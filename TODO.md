# TODO: RTMP / E-RTMP v1 review findings

Scope: `format/rtmp`, `format/flv`, `format/flv/flvio`. One commit per finding.

## A. Critical: a remote client can crash the process or exhaust its memory (no `recover` anywhere)
1. [x] **Panic on a short HEVC ExHeader video message.** In `flvio.go:311-315`, `videoParseHeader` reads the 3-byte CTS (`pio.I24BE(b[n:])`) without a length check. A 5-byte video message `0x91 'h' 'v' 'c' '1'` panics with index-out-of-range. That kills the whole server, because `HandleNetConn` and `Serve` have no `recover`.
2. [x] **Unbounded allocation in an AMF0 strict array.** In `amf0.go:421-424`, `make(AMFArray, count)` uses an untrusted 32-bit count. A count of 0xFFFFFFFF in the `connect` command means about 64 GB, which ends in a fatal OOM before any authentication. The count must be capped by the remaining bytes.
3. [x] **Large pre-allocation per chunk stream.** `chunkStream.Start()` (`rtmp.go:403`) allocates `msgdatalen` (up to 16 MB) as soon as the header arrives. With 16 csids allowed, that is about 256 MB per connection right after the handshake, with nothing to limit it. Allocate gradually, or cap the message size, at least for non-A/V types and before `connect`.
4. [x] **Unbounded AMF0 recursion.** Deeply nested objects/arrays in a 16 MB message can hit Go's 1 GB stack limit, which is fatal. Add a depth limit.
5. [x] **No validation of SetChunkSize.** At `rtmp.go:1951`, 0 makes `readChunk` read 0-byte chunks forever and desync the stream. A value with the MSB set is negative on 32-bit, which panics on the slice. Clamp it to 1..0xFFFFFF and reject 0.
6. [x] **No default timeouts.** `ConnectionIdleTimeout` defaults to 0 on the server. The client has no deadline after dial (`DialTimeout` only covers TCP connect). Idle or slowloris peers can hold goroutines during the handshake and `readConnect` forever.

## B. Protocol correctness bugs (legacy RTMP)
7. [x] **Inconsistent extended timestamp at exactly 0xFFFFFF.** `fillChunkHeader` writes the extended field only when `ts > 0xFFFFFF` (`rtmp.go:1387/1399`, and `writeAVTag:1276`). `fillChunkHeader3` writes it when `ts >= 0xFFFFFF` (`:1436`). The reader (`:1529`) expects the extended field when the 24-bit value is `== 0xFFFFFF`. So at ts = 16777215 ms (about 4h39m) the type 0 header has no extended field but the type 3 chunks do, and the stream is corrupted. Use `>=` everywhere.
8. [x] **Ack sequence number is wrong.** `pollMsg` sends `conn.ackn` and then sets it to 0 (`:500-506`). The spec says the sequence number is the total number of bytes received so far (mod 2^32). Peers that do real windowing (FMS/Wowza-style) will see acks that never move forward. Use the running total (`txrxcount.rxbytes` already has it, but it also needs the handshake bytes).
9. [x] **Server simple handshake sends S2 = zeros.** In `handshakeServer` (`:2137-2139`), `copy(S2, C2)` runs before C2 has been read, so S2 is all zeros. S2 must echo C1. Also, S1 is a copy of C1 rather than the server's own time and random bytes. Clients that verify S2 (librtmp warns) see a mismatch.
10. [x] **Client handshake.** C1 is all zeros (`hsCreate01` is commented out), and S0's version is not checked. The `if ver != 0` branch is dead code, because C2 = S1 either way, so a complex-handshake response is never computed. Some strict servers or CDNs may reject this.
    - [ ] The complex (digest) handshake is still not implemented in the client (the simple handshake is fixed).
11. [ ] **AMF messages are never split into chunks.** `writeAMF0Msg` writes the whole message as one chunk, whatever `writeMaxChunkSize` is. Today this is hidden by the 64 KB chunk size, but metadata over 64 KB (user-supplied via `SetMetaData`) desyncs the peer. It should reuse the chunking loop from `writeAVTag`.
12. [ ] **Messages over 16 MB are not checked.** `PutU24BE` silently truncates the length in `fillChunkHeader`.
13. [ ] **Type 3 continuation with extended timestamp.** At `:1670-1680`, the code peeks 4 bytes and guesses whether they are the extended timestamp. If the payload happens to start with those bytes, it desyncs. It also reads into bufio's own peek buffer (use `Discard(4)`), and those 4 bytes are not added to `n`, so the ack count is low.
14. [ ] **Unhandled control messages.** Abort (2) is not handled; it should drop the partial message on that csid. SetPeerBandwidth (6) is not answered with a WindowAckSize.
15. [ ] **Limit of 16 chunk-stream ids** (`:1490`). This is a reasonable DoS guard but arbitrary. Some encoders use high csids. Make it configurable and document it.
16. [ ] **Type 3 starting a new message after type 0** keeps the same absolute timestamp (delta 0). FFmpeg/SRS add the previous timestamp field as the delta instead. This interop gap feeds the "same timestamp" heuristic in #18.

## C. Timestamp handling
17. [ ] **Drift on non-monotonic timestamps.** At `:1754-1762`, a backward step is ignored and the next forward step adds the full delta, so `xtimenow` drifts ahead. This hits encoders that mux audio and video on one csid, or that jitter. On wraparound, it adds `timenow` but loses `(max - prev)`.
18. [ ] **"Same timestamp" heuristic.** More than 10 equal or backward timestamps among the first 20 messages switch that csid to wall-clock time. This is per csid, so audio can be on wall clock while video keeps encoder time, which desyncs A/V, and the wall clock starts at connect time rather than at the stream's timestamp. After 20 messages, the same pattern drops the whole connection with an error.
19. [ ] **Writer wraps at 2^31.** `writeAVTag` masks `ts & 0x7FFFFFFF`, while RTMP timestamps are 32-bit. Readers that unwrap at 2^32 jump after about 24.8 days.

## D. Command / session layer
20. [ ] **`OnPlayOrPublish` gets no stream info.** It receives only the command name and the connect params, and it runs before `conn.URL` is set (`:686` vs `:716`), so it cannot check the stream key. On rejection, the server still sends **`NetStream.Publish.Start`** and then closes. It should send `NetStream.Publish.BadName` or `Unauthorized` with level `error`.
21. [ ] **No authorization hook for `play`.** `OnPlayOrPublish` is never called in the play branch.
22. [ ] **`objectEncoding` default is wrong.** It defaults to **3** when the client didn't send it (`:625-628`). The server only speaks AMF0, so it should be 0. AMF3 command messages (type 17) are parsed as AMF0 after skipping one byte, so any real AMF3 value (`0x11` avmplus marker) fails. AMF3 data messages (type 15) are ignored.
23. [ ] **`onStatus` transaction id.** It uses the request's transid; the spec says 0. The publish `type` argument is ignored (fine). `releaseStream`, `FCPublish`, `FCUnpublish`, `deleteStream`, `closeStream` and `getStreamLength` get no reply. Some clients wait for `onFCPublish`.
24. [ ] **Commands are ignored after setup.** Once the stream is publishing or playing, commands are dropped (`pollAVTag`). `deleteStream` / `FCUnpublish` from a publisher don't end the stream; only TCP EOF does. In server play mode nobody reads from the socket, so pause, seek and closeStream from players are never seen.
25. [ ] **Client does not handle errors.** `_error` replies to connect or createStream are ignored, so it loops until the peer closes. Transaction ids are not matched. The client doesn't wait for `NetStream.Publish.Start` or `Play.Start`, and it never surfaces `onStatus` errors (BadName, StreamNotFound, UnpublishNotify).
26. [ ] **Client publishing never reads.** Server pings are never answered and server acks and onStatus messages pile up. Some servers drop clients that don't answer pings.
27. [ ] **Client details.** It sends SetPeerBandwidth (normally server-to-client only). SetBufferLength goes to msgsid 0 before the stream exists, so it has no effect. `publish` has no `"live"` argument, and `releaseStream`/`FCPublish` are not sent, which some CDNs require.
28. [ ] **No RTMPS on the client side.** `Dial` is TCP only, and `ParseURL` always defaults to port 1935, with no `rtmps://` or port 443 handling. The server does have TLS.

## E. Enhanced RTMP v1 / FLV layer
29. [ ] **VVC is broken on ingest.** In `flv.go:152-193`, the VVC branch was nested *inside* the `FOURCC_AV1` branch by commit 8b2ed73. Its sequence header is never parsed, so the stream never probes, and VVC frames are emitted with a stale or zero stream index.
30. [ ] **VVC composition time is missing.** VVC `CodedFrames` carries no CTS in parse or fill (only HEVC does, `flvio.go:311,335`). E-RTMP v2, which defines vvc1, says CodedFrames for vvc1 (like avc1 and hvc1) has an SI24 CTS. This breaks interop and loses PTS for B-frames. Either add the CTS or always send `CodedFramesX`. Also, `vvc1` is not in the client's `fourCcList` (`rtmp.go:855`).
31. [ ] **Negative composition time is corrupted on write.** `int32(CompositionTime & 0x7FFFFF)` (`flvio.go:337,347`) clears the sign bit, so −33 is written as +8388575. Use `& 0xFFFFFF`, or pass the int32 straight through.
32. [ ] **Packets get the wrong stream index.** `TagToPacket` ignores FourCC/CodecID and always sets `Idx = VideoStreamIdx`/`AudioStreamIdx`, which defaults to 0, even when no stream of that kind was probed. Video data can end up on the audio stream (unsupported FourCC, legacy VP6/H.263, or no sequence header). `CacheTag` sets the index when the packet is cached, so frames that arrive before their sequence header get the wrong index.
33. [ ] **Legacy CodecID is not checked.** Every non-ExHeader video is treated as AVC (`flvio.go:276`), so H.263, VP6 and Screen Video are parsed and emitted as H.264.
34. [ ] **ExHeader `PacketTypeMetadata` (HDR colorInfo) is dropped by accident.** It keeps the 0x8 bit in FrameType (`flvio.go:271`) and is then filtered out in `handleMsg`. The spec masks FrameType with 0b0111 for all ExHeader packets. Handle it explicitly or skip it on purpose. `SequenceEnd` is also dropped silently.
35. [ ] **Mid-stream sequence headers are dropped.** After probing, a new `SequenceStart` or `AVC_SEQHDR` (resolution or codec change, re-sent config) is discarded, and there is no way to signal new codec data downstream.
36. [ ] **Server doesn't confirm E-RTMP support.** The `connect` reply doesn't echo `fourCcList`, and the client's `fourCcList` is ignored. Not strictly required by v1, but a capability response helps clients decide.
37. [ ] **Prober details.** Speex/Nellymoser frames after the first one, during probing, are dropped. Metadata `audiocodecid`/`videocodecid` turn on HasAudio/HasVideo even for unsupported codecs, which costs 20 packets of probe latency. A failing PushTag is only logged.
38. [ ] **Inconsistent codec support.** `NewMetadataByStreams` rejects NELLYMOSER, while `CodecDataToTag` and `PacketToTag` support it. AAC with more than 2 channels is flagged mono. Neither is harmful, but it's inconsistent.

## F. AMF0
39. [ ] **Strings of exactly 65536 bytes are encoded wrongly.** The check is `<= 65536` (`amf0.go:126,199`), so the uint16 length overflows to 0. It should be `<= 65535`.
40. [ ] **Unsupported Go types encode to 0 bytes silently.** For example `map[string]interface{}`, `[]interface{}`, pointers. Inside an AMFMap, the key is then written with no value, which corrupts the message. That is reachable through `SetMetaData`. Return an error or panic.
41. [ ] **Unsupported markers.** Reference, typed-object and AMF3-switch markers fail the whole message. A metadata parse error kills the connection.

## G. Concurrency / lifecycle / API
42. [ ] **Data race on the byte counters.** `TxBytes`/`RxBytes` are read from other goroutines without atomics.
43. [ ] **`Conn` is not safe for concurrent reads and writes.** The read path writes Acks and Ping responses through the shared `bufw` and `writebuf`. Any user who reads and writes at the same time (needed to fix #24/#26) will corrupt output. A write mutex is needed.
44. [ ] **Unsynchronized server shutdown.** `Server.Close` races with `Serve` on `listener`/`doneChan`, and calling `Close` twice can panic. Active connections are not closed.
45. [ ] **Leaking `Handler` helpers.** `ServerMuxer` and `ServerDemuxer` leak the server, and they block forever on unbuffered channels for a second client or when `ListenAndServe` returns.
46. [ ] **Minor:**
   - `writeAVTag` flushes after every frame, which is one syscall per frame.
   - The error is logged twice (`HandleNetConn` and `Serve`).
   - `writeConnect` duplicates the WindowAckSize handling.
   - `writeAVTag` gives csid 0 to non-A/V tags.
   - `SplitPath` puts everything after the first segment into the stream name (`app/instance` ambiguity).
   - There are no unit tests in `format/rtmp`.

## H. Other packages
47. [ ] **`pktque.Walltime` never paces packets.** `av/pktque/filters.go`: `ModifyPacket` sets the start time only if `firsttime == math.MinInt64`, but the zero value of `firsttime` is 0, so the start time is never set and the filter never sleeps. A publisher using it (e.g. `examples/rtmp_publish`) sends a whole file at once instead of in real time. It also uses `time.Now().Unix() * 1000`, which has only second resolution.
