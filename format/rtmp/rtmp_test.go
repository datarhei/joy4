package rtmp

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"testing"
	"time"

	"github.com/datarhei/joy4/format/flv/flvio"
	"github.com/datarhei/joy4/utils/bits/pio"
	"github.com/stretchr/testify/require"
)

// newReadConn returns a connection that reads the given data. Written data (e.g. acks) is discarded.
func newReadConn(data []byte) *Conn {
	c1, _ := net.Pipe()
	conn := NewConn(c1)
	conn.bufr = bufio.NewReader(bytes.NewReader(data))
	conn.bufw = bufio.NewWriter(io.Discard)
	return conn
}

// newWriteConn returns a connection that writes into the returned buffer.
func newWriteConn() (*Conn, *bytes.Buffer) {
	c1, _ := net.Pipe()
	conn := NewConn(c1)
	buf := &bytes.Buffer{}
	conn.bufw = bufio.NewWriter(buf)
	return conn, buf
}

// chunkHeader0 returns a type 0 chunk header (timestamp < 0xFFFFFF).
func chunkHeader0(csid uint8, timestamp uint32, msglen int, msgtypeid uint8, msgsid uint32) []byte {
	b := make([]byte, 12)
	b[0] = csid & 0x3f
	pio.PutU24BE(b[1:], timestamp)
	pio.PutU24BE(b[4:], uint32(msglen))
	b[7] = msgtypeid
	pio.PutU32LE(b[8:], msgsid)
	return b
}

// chunkMessage splits the message into chunks of the given size.
func chunkMessage(csid uint8, timestamp uint32, msgtypeid uint8, msgsid uint32, payload []byte, chunksize int) []byte {
	data := chunkHeader0(csid, timestamp, len(payload), msgtypeid, msgsid)
	for i := 0; i < len(payload); i += chunksize {
		if i != 0 {
			data = append(data, 0xc0|(csid&0x3f))
		}
		end := i + chunksize
		if end > len(payload) {
			end = len(payload)
		}
		data = append(data, payload[i:end]...)
	}
	return data
}

func TestReadChunkAllocatesReceivedDataOnly(t *testing.T) {
	// announce a message of 16 MB, but send only the first chunk
	data := chunkHeader0(3, 0, 0xFFFFFF, msgtypeidCommandMsgAMF0, 0)
	data = append(data, make([]byte, 128)...)

	conn := newReadConn(data)

	err := conn.readChunk()
	require.NoError(t, err)

	cs := conn.readcsmap[3]
	require.NotNil(t, cs)
	require.Equal(t, 128, len(cs.msgdata))
	require.LessOrEqual(t, cap(cs.msgdata), 2*128)
	require.Equal(t, uint32(0xFFFFFF-128), cs.msgdataleft)

	err = conn.readChunk()
	require.Error(t, err)
}

func TestReadChunkReassemblesMessage(t *testing.T) {
	// AVC NALU, keyframe
	payload := []byte{0x17, 0x01, 0x00, 0x00, 0x00}
	for i := 0; i < 1000; i++ {
		payload = append(payload, byte(i))
	}

	data := chunkMessage(6, 40, msgtypeidVideoMsg, 1, payload, 128)
	conn := newReadConn(data)

	err := conn.pollMsg()
	require.NoError(t, err)
	require.Equal(t, uint8(msgtypeidVideoMsg), conn.msgtypeid)
	require.Equal(t, payload, conn.msgdata)
	require.Equal(t, payload[5:], conn.avtag.Data)
	require.Equal(t, int64(40), conn.timestamp)
}

func TestReadChunkEmptyMessage(t *testing.T) {
	data := chunkHeader0(3, 0, 0, msgtypeidDataMsgAMF0, 1)
	conn := newReadConn(data)

	err := conn.pollMsg()
	require.NoError(t, err)
	require.Equal(t, uint8(msgtypeidDataMsgAMF0), conn.msgtypeid)
	require.Empty(t, conn.msgdata)
}

func setChunkSizeMessage(size uint32) []byte {
	payload := make([]byte, 4)
	pio.PutU32BE(payload, size)
	return chunkMessage(2, 0, msgtypeidSetChunkSize, 0, payload, 128)
}

func TestSetChunkSize(t *testing.T) {
	conn := newReadConn(setChunkSizeMessage(4096))

	err := conn.pollMsg()
	require.NoError(t, err)
	require.Equal(t, 4096, conn.readMaxChunkSize)
}

func TestSetChunkSizeInvalid(t *testing.T) {
	for _, size := range []uint32{0, 0x80000000, 0xFFFFFFFF} {
		conn := newReadConn(setChunkSizeMessage(size))

		err := conn.pollMsg()
		require.Error(t, err, "size %#x", size)
		require.Equal(t, 128, conn.readMaxChunkSize, "size %#x", size)
	}
}

func TestSetChunkSizeTooLarge(t *testing.T) {
	conn := newReadConn(setChunkSizeMessage(0x7FFFFFFF))

	err := conn.pollMsg()
	require.NoError(t, err)
	require.Equal(t, 0xFFFFFF, conn.readMaxChunkSize)
}

func TestSetChunkSizeApplied(t *testing.T) {
	// after a SetChunkSize of 1000, a 1000 byte message arrives in a single chunk
	payload := []byte{0x17, 0x01, 0x00, 0x00, 0x00}
	payload = append(payload, make([]byte, 995)...)

	data := setChunkSizeMessage(1000)
	data = append(data, chunkMessage(6, 0, msgtypeidVideoMsg, 1, payload, 1000)...)
	conn := newReadConn(data)

	err := conn.pollMsg()
	require.NoError(t, err)

	err = conn.pollMsg()
	require.NoError(t, err)
	require.Equal(t, payload, conn.msgdata)
}

func TestServerSetupTimeout(t *testing.T) {
	c1, c2 := net.Pipe()
	defer c2.Close()

	server := &Server{
		SetupTimeout: 100 * time.Millisecond,
	}

	start := time.Now()

	// the client never sends the handshake
	err := server.HandleNetConn(c1)
	require.True(t, errors.Is(err, os.ErrDeadlineExceeded), "%v", err)
	require.Less(t, time.Since(start), 2*time.Second)
}

func TestClientSetupTimeout(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer ln.Close()

	// the server accepts the connection but never answers
	go func() {
		c, err := ln.Accept()
		if err != nil {
			return
		}
		defer c.Close()
		time.Sleep(time.Second)
	}()

	conn, err := Dial("rtmp://"+ln.Addr().String()+"/live/test", DialOptions{
		SetupTimeout: 100 * time.Millisecond,
	})
	require.NoError(t, err)
	defer conn.Close()

	start := time.Now()

	err = conn.Prepare()
	require.True(t, errors.Is(err, os.ErrDeadlineExceeded), "%v", err)
	require.Less(t, time.Since(start), 2*time.Second)
}

func TestSetupTimeoutRemovedAfterSetup(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer ln.Close()

	setupTimeout := 100 * time.Millisecond

	server := &Server{
		SetupTimeout: setupTimeout,
	}

	result := make(chan error, 1)

	server.HandlePublish = func(conn *Conn) {
		// wait longer than the setup timeout before reading
		time.Sleep(3 * setupTimeout)
		result <- conn.pollMsg()
	}

	go func() {
		c, err := ln.Accept()
		if err != nil {
			result <- err
			return
		}
		server.HandleNetConn(c)
	}()

	conn, err := Dial("rtmp://"+ln.Addr().String()+"/live/test", DialOptions{
		SetupTimeout: setupTimeout,
	})
	require.NoError(t, err)
	defer conn.Close()

	err = conn.Prepare()
	require.NoError(t, err)
	require.True(t, conn.publishing)

	// the client waits longer than the setup timeout before writing
	time.Sleep(3 * setupTimeout)

	err = conn.writeSetChunkSize(4096, false)
	require.NoError(t, err)
	err = conn.flushWrite()
	require.NoError(t, err)

	select {
	case err := <-result:
		require.NoError(t, err)
	case <-time.After(5 * time.Second):
		require.Fail(t, "timeout waiting for the server")
	}
}

func TestWriteAVTagExtendedTimestamp(t *testing.T) {
	data := make([]byte, 1000)
	for i := range data {
		data[i] = byte(i)
	}

	tag := flvio.Tag{
		Type:          flvio.TAG_VIDEO,
		FrameType:     flvio.FRAME_KEY,
		CodecID:       flvio.VIDEO_H264,
		AVCPacketType: flvio.AVC_NALU,
		Data:          data,
	}

	for _, ts := range []int64{0, 0xFFFFFE, 0xFFFFFF, 0x1000000, 0x7FFFFF00, 0x7FFFFFFF, 0xFFFFFFFF} {
		wconn, buf := newWriteConn()
		wconn.avmsgsid = 1

		// the message is bigger than the chunk size, such that type 3 chunks follow
		err := wconn.writeAVTag(tag, ts)
		require.NoError(t, err, "ts %#x", ts)

		// a following message must be read correctly as well
		err = wconn.writeAVTag(tag, ts+40)
		require.NoError(t, err, "ts %#x", ts)

		rconn := newReadConn(buf.Bytes())

		err = rconn.pollMsg()
		require.NoError(t, err, "ts %#x", ts)
		require.Equal(t, uint8(msgtypeidVideoMsg), rconn.msgtypeid, "ts %#x", ts)
		require.Equal(t, ts, rconn.timestamp, "ts %#x", ts)
		require.Equal(t, data, rconn.avtag.Data, "ts %#x", ts)

		err = rconn.pollMsg()
		require.NoError(t, err, "ts %#x", ts)
		require.Equal(t, uint8(msgtypeidVideoMsg), rconn.msgtypeid, "ts %#x", ts)
		require.Equal(t, ts+40, rconn.timestamp, "ts %#x", ts)
		require.Equal(t, data, rconn.avtag.Data, "ts %#x", ts)
	}
}

func TestFillChunkHeaderExtendedTimestamp(t *testing.T) {
	conn, _ := newWriteConn()

	for _, tc := range []struct {
		timestamp uint32
		length    int
	}{
		{0xFFFFFE, 12},
		{0xFFFFFF, 16},
		{0x1000000, 16},
	} {
		b := make([]byte, 16)
		n := conn.fillChunkHeader(false, b, 3, tc.timestamp, msgtypeidCommandMsgAMF0, 0, 0)
		require.Equal(t, tc.length, n, "ts %#x", tc.timestamp)

		if tc.length == 16 {
			require.Equal(t, maxFlvTimestamp, pio.U24BE(b[1:]), "ts %#x", tc.timestamp)
			require.Equal(t, tc.timestamp, pio.U32BE(b[12:]), "ts %#x", tc.timestamp)
		} else {
			require.Equal(t, tc.timestamp, pio.U24BE(b[1:]), "ts %#x", tc.timestamp)
		}
	}
}

// readAcks returns the sequence numbers of all acks in the data.
func readAcks(t *testing.T, data []byte) []uint32 {
	acks := []uint32{}

	for len(data) > 0 {
		require.GreaterOrEqual(t, len(data), 16)
		require.Equal(t, byte(0x02), data[0]) // type 0, csid 2
		require.Equal(t, byte(msgtypeidAck), data[7])
		require.Equal(t, uint32(4), pio.U24BE(data[4:]))
		acks = append(acks, pio.U32BE(data[12:]))
		data = data[16:]
	}

	return acks
}

// ackTestData returns count data messages of 1012 bytes each (12 bytes header, 1000 AMF0 null values).
func ackTestData(count int) []byte {
	data := []byte{}
	for i := 0; i < count; i++ {
		data = append(data, chunkMessage(3, 0, msgtypeidDataMsgAMF0, 0, bytes.Repeat([]byte{0x05}, 1000), 1000)...)
	}
	return data
}

func TestAckSequenceNumber(t *testing.T) {
	conn := newReadConn(ackTestData(3))
	buf := &bytes.Buffer{}
	conn.bufw = bufio.NewWriter(buf)
	conn.readMaxChunkSize = 1000
	conn.readAckSize = 1000

	for i := 0; i < 3; i++ {
		err := conn.pollMsg()
		require.NoError(t, err)
	}

	// the sequence number is the total of the received bytes, not the bytes since the last ack
	require.Equal(t, []uint32{1012, 2024, 3036}, readAcks(t, buf.Bytes()))
}

func TestAckSequenceNumberWrap(t *testing.T) {
	conn := newReadConn(ackTestData(2))
	buf := &bytes.Buffer{}
	conn.bufw = bufio.NewWriter(buf)
	conn.readMaxChunkSize = 1000
	conn.readAckSize = 1000
	conn.acks = 0xFFFFFC00

	for i := 0; i < 2; i++ {
		err := conn.pollMsg()
		require.NoError(t, err)
	}

	require.Equal(t, []uint32{0xFFFFFFF4, 0x3E8}, readAcks(t, buf.Bytes()))
}

func TestAckSequenceNumberHandshake(t *testing.T) {
	c1, c2 := net.Pipe()
	defer c1.Close()
	defer c2.Close()

	server := NewConn(c1)
	server.isserver = true
	client := NewConn(c2)

	result := make(chan error, 1)

	go func() {
		result <- server.handshakeServer()
	}()

	err := client.handshakeClient()
	require.NoError(t, err)
	// C2 is written with the following commands
	err = client.flushWrite()
	require.NoError(t, err)

	err = <-result
	require.NoError(t, err)

	// C0 + C1 + C2 and S0 + S1 + S2
	require.Equal(t, uint32(1+1536+1536), server.acks)
	require.Equal(t, uint32(1+1536+1536), client.acks)
}

func TestHandshakeServerSimple(t *testing.T) {
	c1, c2 := net.Pipe()
	defer c1.Close()
	defer c2.Close()

	server := NewConn(c1)
	server.isserver = true

	result := make(chan error, 1)

	go func() {
		result <- server.handshakeServer()
	}()

	// C0 + C1 of a simple handshake (version 0)
	C0C1 := make([]byte, 1+1536)
	C0C1[0] = 3
	C1 := C0C1[1:]
	pio.PutU32BE(C1[0:4], 0x11223344)
	pio.PutU32BE(C1[4:8], 0)
	for i := 8; i < len(C1); i++ {
		C1[i] = byte(i)
	}

	_, err := c2.Write(C0C1)
	require.NoError(t, err)

	S0S1S2 := make([]byte, 1+1536+1536)
	_, err = io.ReadFull(c2, S0S1S2)
	require.NoError(t, err)

	S0 := S0S1S2[0]
	S1 := S0S1S2[1 : 1+1536]
	S2 := S0S1S2[1+1536:]

	require.Equal(t, byte(3), S0)

	// S1 contains the time of the server, zero and random bytes
	require.Equal(t, uint32(0), pio.U32BE(S1[4:8]))
	require.NotEqual(t, C1[8:], S1[8:])

	// S2 is an exact echo of C1
	require.Equal(t, C1, S2)

	// C2 is an echo of S1
	_, err = c2.Write(S1)
	require.NoError(t, err)

	err = <-result
	require.NoError(t, err)
}

// handshakeClient runs the client handshake against a server that answers with the given S0
// and returns the received C0C1 and C2, and the error of the handshake.
func handshakeClient(t *testing.T, S0 byte) ([]byte, []byte, error) {
	c1, c2 := net.Pipe()
	defer c1.Close()
	defer c2.Close()

	client := NewConn(c1)

	result := make(chan error, 1)

	go func() {
		err := client.handshakeClient()
		if err == nil {
			// C2 is written with the following commands
			err = client.flushWrite()
		}
		result <- err
	}()

	C0C1 := make([]byte, 1+1536)
	_, err := io.ReadFull(c2, C0C1)
	require.NoError(t, err)

	// S0 + S1 + S2 of a simple handshake
	S0S1S2 := make([]byte, 1+1536+1536)
	S0S1S2[0] = S0
	S1 := S0S1S2[1 : 1+1536]
	pio.PutU32BE(S1[0:4], 0x55667788)
	for i := 8; i < len(S1); i++ {
		S1[i] = byte(i * 3)
	}
	copy(S0S1S2[1+1536:], C0C1[1:])

	_, err = c2.Write(S0S1S2)
	require.NoError(t, err)

	if S0 != 3 {
		return C0C1, nil, <-result
	}

	C2 := make([]byte, 1536)
	_, err = io.ReadFull(c2, C2)
	require.NoError(t, err)

	require.Equal(t, S1, C2)

	return C0C1, C2, <-result
}

func TestHandshakeClientSimple(t *testing.T) {
	C0C1, _, err := handshakeClient(t, 3)
	require.NoError(t, err)

	C0 := C0C1[0]
	C1 := C0C1[1:]

	require.Equal(t, byte(3), C0)

	// C1 contains the time of the client, zero and random bytes
	require.Equal(t, uint32(0), pio.U32BE(C1[4:8]))
	require.NotEqual(t, make([]byte, 1528), C1[8:])
}

func TestHandshakeClientInvalidVersion(t *testing.T) {
	_, _, err := handshakeClient(t, 6)
	require.Error(t, err)
}

func TestWriteAVTagChunks(t *testing.T) {
	// the tag header of an AVC NALU is 5 bytes, the payloads are 1005 and exactly 3 chunks
	for _, datalen := range []int{1000, 3*128 - 5} {
		data := make([]byte, datalen)
		for i := range data {
			data[i] = byte(i)
		}

		tag := flvio.Tag{
			Type:          flvio.TAG_VIDEO,
			FrameType:     flvio.FRAME_KEY,
			CodecID:       flvio.VIDEO_H264,
			AVCPacketType: flvio.AVC_NALU,
			Data:          data,
		}

		conn, buf := newWriteConn()
		conn.avmsgsid = 1

		err := conn.writeAVTag(tag, 40)
		require.NoError(t, err)

		payload := append([]byte{0x17, 0x01, 0x00, 0x00, 0x00}, data...)
		require.Equal(t, chunkMessage(7, 40, msgtypeidVideoMsg, 1, payload, 128), buf.Bytes(), "datalen %d", datalen)
	}
}

func TestWriteAVTagChunkSizeSmallerThanTagHeader(t *testing.T) {
	tag := flvio.Tag{
		Type:          flvio.TAG_VIDEO,
		FrameType:     flvio.FRAME_KEY,
		CodecID:       flvio.VIDEO_H264,
		AVCPacketType: flvio.AVC_NALU,
		Data:          []byte{1, 2, 3},
	}

	conn, buf := newWriteConn()
	conn.avmsgsid = 1
	conn.writeMaxChunkSize = 1

	err := conn.writeAVTag(tag, 40)
	require.NoError(t, err)

	payload := []byte{0x17, 0x01, 0x00, 0x00, 0x00, 1, 2, 3}
	require.Equal(t, chunkMessage(7, 40, msgtypeidVideoMsg, 1, payload, 1), buf.Bytes())
}

func TestWriteDataMsgChunks(t *testing.T) {
	// a single key, such that the encoding doesn't depend on the iteration order of the map
	metadata := flvio.AMFMap{
		"comment": string(bytes.Repeat([]byte{'x'}, 5000)),
	}

	conn, buf := newWriteConn()

	err := conn.writeDataMsg(5, 1, "onMetaData", metadata)
	require.NoError(t, err)
	err = conn.flushWrite()
	require.NoError(t, err)

	payload := make([]byte, flvio.LenAMF0Val("onMetaData")+flvio.LenAMF0Val(metadata))
	n := flvio.FillAMF0Val(payload, "onMetaData")
	flvio.FillAMF0Val(payload[n:], metadata)

	require.Greater(t, len(payload), conn.writeMaxChunkSize)
	require.Equal(t, chunkMessage(5, 0, msgtypeidDataMsgAMF0, 1, payload, 128), buf.Bytes())

	rconn := newReadConn(buf.Bytes())

	err = rconn.pollMsg()
	require.NoError(t, err)
	require.Equal(t, metadata, rconn.metadata)
}

func TestWriteDataMsgChunksReadBack(t *testing.T) {
	metadata := flvio.AMFMap{
		"width":   1280.0,
		"height":  720.0,
		"comment": string(bytes.Repeat([]byte{'x'}, 5000)),
	}

	conn, buf := newWriteConn()

	err := conn.writeDataMsg(5, 1, "onMetaData", metadata)
	require.NoError(t, err)
	err = conn.flushWrite()
	require.NoError(t, err)

	rconn := newReadConn(buf.Bytes())

	err = rconn.pollMsg()
	require.NoError(t, err)
	require.Equal(t, metadata, rconn.metadata)
}

func TestWriteCommandMsgChunks(t *testing.T) {
	description := string(bytes.Repeat([]byte{'y'}, 1000))

	conn, buf := newWriteConn()

	// type 0 chunk header
	err := conn.writeCommandMsg(false, 3, 0, "_result", 1, nil, flvio.AMFMap{"description": description})
	require.NoError(t, err)
	// type 1 chunk header
	err = conn.writeCommandMsg(true, 3, 0, "onStatus", 0, nil, flvio.AMFMap{"description": description})
	require.NoError(t, err)
	err = conn.flushWrite()
	require.NoError(t, err)

	rconn := newReadConn(buf.Bytes())

	err = rconn.pollMsg()
	require.NoError(t, err)
	require.True(t, rconn.gotcommand)
	require.Equal(t, "_result", rconn.commandname)
	require.Equal(t, []interface{}{flvio.AMFMap{"description": description}}, rconn.commandparams)

	err = rconn.pollMsg()
	require.NoError(t, err)
	require.True(t, rconn.gotcommand)
	require.Equal(t, "onStatus", rconn.commandname)
	require.Equal(t, []interface{}{flvio.AMFMap{"description": description}}, rconn.commandparams)
}

func TestWriteAVTagMaxMessageLength(t *testing.T) {
	// the tag header of an AVC NALU is 5 bytes
	tag := flvio.Tag{
		Type:          flvio.TAG_VIDEO,
		FrameType:     flvio.FRAME_KEY,
		CodecID:       flvio.VIDEO_H264,
		AVCPacketType: flvio.AVC_NALU,
		Data:          make([]byte, maxMessageLength-5),
	}

	conn, buf := newWriteConn()
	conn.avmsgsid = 1
	conn.writeMaxChunkSize = 65536

	err := conn.writeAVTag(tag, 40)
	require.NoError(t, err)

	rconn := newReadConn(buf.Bytes())
	rconn.readMaxChunkSize = 65536

	err = rconn.pollMsg()
	require.NoError(t, err)
	require.Equal(t, maxMessageLength, len(rconn.msgdata))
	require.Equal(t, len(tag.Data), len(rconn.avtag.Data))

	// one byte more
	tag.Data = make([]byte, maxMessageLength-4)

	conn, buf = newWriteConn()
	conn.avmsgsid = 1

	err = conn.writeAVTag(tag, 40)
	require.Error(t, err)
	err = conn.flushWrite()
	require.NoError(t, err)
	require.Equal(t, 0, buf.Len())
}

func TestWriteDataMsgMaxMessageLength(t *testing.T) {
	// a long string has a 5 byte header
	value := string(bytes.Repeat([]byte{'x'}, maxMessageLength-5))

	conn, buf := newWriteConn()
	conn.writeMaxChunkSize = 65536

	err := conn.writeDataMsg(5, 1, value)
	require.NoError(t, err)
	err = conn.flushWrite()
	require.NoError(t, err)

	rconn := newReadConn(buf.Bytes())
	rconn.readMaxChunkSize = 65536

	err = rconn.pollMsg()
	require.NoError(t, err)
	require.Equal(t, maxMessageLength, len(rconn.msgdata))
	require.Equal(t, []interface{}{value}, rconn.datamsgvals)

	// one byte more
	conn, buf = newWriteConn()

	err = conn.writeDataMsg(5, 1, value+"x")
	require.Error(t, err)
	err = conn.flushWrite()
	require.NoError(t, err)
	require.Equal(t, 0, buf.Len())
}

// chunkMessageExt splits the message into chunks of the given size, with an extended timestamp in
// the type 0 chunk header. If repeat is true, the extended timestamp is repeated in the type 3 chunks.
func chunkMessageExt(csid uint8, timestamp uint32, msgtypeid uint8, msgsid uint32, payload []byte, chunksize int, repeat bool) []byte {
	data := chunkHeader0(csid, maxFlvTimestamp, len(payload), msgtypeid, msgsid)
	data = append(data, 0, 0, 0, 0)
	pio.PutU32BE(data[12:], timestamp)
	for i := 0; i < len(payload); i += chunksize {
		if i != 0 {
			data = append(data, 0xc0|(csid&0x3f))
			if repeat {
				data = append(data, 0, 0, 0, 0)
				pio.PutU32BE(data[len(data)-4:], timestamp)
			}
		}
		end := i + chunksize
		if end > len(payload) {
			end = len(payload)
		}
		data = append(data, payload[i:end]...)
	}
	return data
}

// extTimestampPayloads returns two video messages of 200 bytes. The continuation chunk (at 128) of the
// second message starts with the bytes of the extended timestamp.
func extTimestampPayloads(ts2 uint32) ([]byte, []byte) {
	payload1 := append([]byte{0x17, 0x01, 0x00, 0x00, 0x00}, bytes.Repeat([]byte{0xAA}, 195)...)
	payload2 := append([]byte{0x17, 0x01, 0x00, 0x00, 0x00}, bytes.Repeat([]byte{0xBB}, 195)...)
	pio.PutU32BE(payload2[128:], ts2)
	return payload1, payload2
}

func TestReadChunkExtendedTimestampNotRepeated(t *testing.T) {
	ts1, ts2 := uint32(0x01000000), uint32(0x01000028)
	payload1, payload2 := extTimestampPayloads(ts2)

	data := chunkMessageExt(6, ts1, msgtypeidVideoMsg, 1, payload1, 128, false)
	data = append(data, chunkMessageExt(6, ts2, msgtypeidVideoMsg, 1, payload2, 128, false)...)
	conn := newReadConn(data)

	err := conn.pollMsg()
	require.NoError(t, err)
	require.Equal(t, payload1, conn.msgdata)
	require.Equal(t, int64(ts1), conn.timestamp)

	// the data of the continuation chunk must not be taken as extended timestamp
	err = conn.pollMsg()
	require.NoError(t, err)
	require.Equal(t, payload2, conn.msgdata)
	require.Equal(t, int64(ts2), conn.timestamp)

	require.Equal(t, uint32(len(data)), conn.acks)
}

func TestReadChunkExtendedTimestampRepeated(t *testing.T) {
	ts1, ts2 := uint32(0x01000000), uint32(0x01000028)
	payload1, payload2 := extTimestampPayloads(ts2)

	data := chunkMessageExt(6, ts1, msgtypeidVideoMsg, 1, payload1, 128, true)
	data = append(data, chunkMessageExt(6, ts2, msgtypeidVideoMsg, 1, payload2, 128, true)...)
	conn := newReadConn(data)

	err := conn.pollMsg()
	require.NoError(t, err)
	require.Equal(t, payload1, conn.msgdata)
	require.Equal(t, int64(ts1), conn.timestamp)

	// only the repeated extended timestamp is skipped, not the data of the continuation chunk
	err = conn.pollMsg()
	require.NoError(t, err)
	require.Equal(t, payload2, conn.msgdata)
	require.Equal(t, int64(ts2), conn.timestamp)

	// the skipped extended timestamps are counted
	require.Equal(t, uint32(len(data)), conn.acks)
}

func abortMessage(csid uint32) []byte {
	payload := make([]byte, 4)
	pio.PutU32BE(payload, csid)
	return chunkMessage(2, 0, msgtypeidAbort, 0, payload, 128)
}

func TestAbort(t *testing.T) {
	aborted := append([]byte{0x17, 0x01, 0x00, 0x00, 0x00}, bytes.Repeat([]byte{0xAA}, 295)...)
	payload := append([]byte{0x17, 0x01, 0x00, 0x00, 0x00}, bytes.Repeat([]byte{0xBB}, 195)...)

	// only the first chunk of the aborted message
	data := chunkMessage(6, 0, msgtypeidVideoMsg, 1, aborted, 128)[:12+128]
	data = append(data, abortMessage(6)...)
	data = append(data, chunkMessage(6, 40, msgtypeidVideoMsg, 1, payload, 128)...)

	conn := newReadConn(data)

	err := conn.pollMsg()
	require.NoError(t, err)
	require.Equal(t, uint8(msgtypeidAbort), conn.msgtypeid)

	err = conn.pollMsg()
	require.NoError(t, err)
	require.Equal(t, uint8(msgtypeidVideoMsg), conn.msgtypeid)
	require.Equal(t, payload, conn.msgdata)
	require.Equal(t, int64(40), conn.timestamp)
}

func TestAbortType3(t *testing.T) {
	aborted := append([]byte{0x17, 0x01, 0x00, 0x00, 0x00}, bytes.Repeat([]byte{0xAA}, 295)...)
	payload := append([]byte{0x17, 0x01, 0x00, 0x00, 0x00}, bytes.Repeat([]byte{0xBB}, 295)...)

	// only the first chunk of the aborted message
	data := chunkMessage(6, 0, msgtypeidVideoMsg, 1, aborted, 128)[:12+128]
	data = append(data, abortMessage(6)...)
	// a new message of the same length with type 3 chunk headers only
	for i := 0; i < len(payload); i += 128 {
		end := i + 128
		if end > len(payload) {
			end = len(payload)
		}
		data = append(data, 0xc6)
		data = append(data, payload[i:end]...)
	}

	conn := newReadConn(data)

	err := conn.pollMsg()
	require.NoError(t, err)
	require.Equal(t, uint8(msgtypeidAbort), conn.msgtypeid)

	err = conn.pollMsg()
	require.NoError(t, err)
	require.Equal(t, uint8(msgtypeidVideoMsg), conn.msgtypeid)
	require.Equal(t, payload, conn.msgdata)
}

func TestAbortUnknownChunkStream(t *testing.T) {
	conn := newReadConn(abortMessage(42))

	err := conn.pollMsg()
	require.NoError(t, err)
	require.Equal(t, uint8(msgtypeidAbort), conn.msgtypeid)
}

func setPeerBandwidthMessage(size uint32, limit uint8) []byte {
	payload := make([]byte, 5)
	pio.PutU32BE(payload, size)
	payload[4] = limit
	return chunkMessage(2, 0, msgtypeidSetPeerBandwidth, 0, payload, 128)
}

// readWindowAckSizes returns the window sizes of all WindowAckSize messages in the data.
func readWindowAckSizes(t *testing.T, data []byte) []uint32 {
	sizes := []uint32{}

	for len(data) > 0 {
		require.GreaterOrEqual(t, len(data), 16)
		require.Equal(t, byte(0x02), data[0]) // type 0, csid 2
		require.Equal(t, byte(msgtypeidWindowAckSize), data[7])
		sizes = append(sizes, pio.U32BE(data[12:]))
		data = data[16:]
	}

	return sizes
}

func TestSetPeerBandwidth(t *testing.T) {
	steps := []struct {
		size   uint32
		limit  uint8
		answer []uint32
	}{
		{1000000, peerBWLimitDynamic, []uint32{}},        // no previous limit, ignored
		{3000000, peerBWLimitSoft, []uint32{}},           // bigger than the current window
		{1000000, peerBWLimitSoft, []uint32{1000000}},    // smaller than the current window
		{4000000, peerBWLimitHard, []uint32{4000000}},    // always applied
		{4000000, peerBWLimitHard, []uint32{}},           // unchanged
		{5000000, peerBWLimitDynamic, []uint32{5000000}}, // previous limit was hard
		{6000000, peerBWLimitSoft, []uint32{}},           // bigger than the current window
		{1000000, peerBWLimitDynamic, []uint32{}},        // previous limit was soft, ignored
	}

	data := []byte{}
	for _, step := range steps {
		data = append(data, setPeerBandwidthMessage(step.size, step.limit)...)
	}

	conn := newReadConn(data)
	buf := &bytes.Buffer{}
	conn.bufw = bufio.NewWriter(buf)

	// the window ack size sent with the basic configuration
	err := conn.writeWindowAckSize(2500000, false)
	require.NoError(t, err)
	err = conn.flushWrite()
	require.NoError(t, err)
	buf.Reset()

	for i, step := range steps {
		err := conn.pollMsg()
		require.NoError(t, err, "step %d", i)
		require.Equal(t, uint8(msgtypeidSetPeerBandwidth), conn.msgtypeid, "step %d", i)
		require.Equal(t, step.answer, readWindowAckSizes(t, buf.Bytes()), "step %d", i)
		buf.Reset()
	}
}

func TestSetPeerBandwidthInvalid(t *testing.T) {
	conn := newReadConn(setPeerBandwidthMessage(1000000, 3))

	err := conn.pollMsg()
	require.Error(t, err)
}

// chunkStreamsData returns one data message (an AMF0 null) on each of count chunk streams, starting with csid 3.
func chunkStreamsData(count int) []byte {
	data := []byte{}
	for i := 0; i < count; i++ {
		data = append(data, chunkMessage(uint8(3+i), 0, msgtypeidDataMsgAMF0, 0, []byte{0x05}, 128)...)
	}
	return data
}

func TestMaxChunkStreams(t *testing.T) {
	conn := newReadConn(chunkStreamsData(DefaultMaxChunkStreams + 1))

	for i := 0; i < DefaultMaxChunkStreams; i++ {
		err := conn.pollMsg()
		require.NoError(t, err, "chunk stream %d", i)
	}

	err := conn.pollMsg()
	require.ErrorContains(t, err, "too many chunk streams")
	require.Equal(t, DefaultMaxChunkStreams, len(conn.readcsmap))
}

func TestMaxChunkStreamsKnownChunkStream(t *testing.T) {
	// more messages on already known chunk streams are fine
	data := chunkStreamsData(DefaultMaxChunkStreams)
	data = append(data, chunkStreamsData(DefaultMaxChunkStreams)...)
	conn := newReadConn(data)

	for i := 0; i < 2*DefaultMaxChunkStreams; i++ {
		err := conn.pollMsg()
		require.NoError(t, err, "message %d", i)
	}
}

func TestMaxChunkStreamsConfigured(t *testing.T) {
	conn := newReadConn(chunkStreamsData(5))
	conn.maxChunkStreams = 4

	for i := 0; i < 4; i++ {
		err := conn.pollMsg()
		require.NoError(t, err, "chunk stream %d", i)
	}

	err := conn.pollMsg()
	require.ErrorContains(t, err, "max. 4 allowed")
}

func TestMaxChunkStreamsOptions(t *testing.T) {
	c1, c2 := net.Pipe()
	defer c2.Close()

	maxChunkStreams := make(chan int, 1)

	server := &Server{
		MaxChunkStreams: 4,
		HandleConn: func(conn *Conn) {
			maxChunkStreams <- conn.maxChunkStreams
		},
	}

	err := server.HandleNetConn(c1)
	require.NoError(t, err)
	require.Equal(t, 4, <-maxChunkStreams)

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer ln.Close()

	conn, err := Dial("rtmp://"+ln.Addr().String()+"/live/test", DialOptions{MaxChunkStreams: 5})
	require.NoError(t, err)
	defer conn.Close()
	require.Equal(t, 5, conn.maxChunkStreams)

	conn2, err := Dial("rtmp://"+ln.Addr().String()+"/live/test", DialOptions{})
	require.NoError(t, err)
	defer conn2.Close()
	require.Equal(t, DefaultMaxChunkStreams, conn2.maxChunkStreams)
}

// type3Message returns a message with a type 3 chunk header only (fits in one chunk).
func type3Message(csid uint8, payload []byte) []byte {
	return append([]byte{0xc0 | (csid & 0x3f)}, payload...)
}

func TestReadChunkType3AfterType0(t *testing.T) {
	// audio messages of the same size, every 20 ms, starting at 20 ms (RTMP spec 5.3.1.2.4)
	payload := append([]byte{0x2e}, bytes.Repeat([]byte{0xCC}, 99)...)

	data := chunkMessage(4, 20, msgtypeidAudioMsg, 1, payload, 128)
	data = append(data, type3Message(4, payload)...)
	data = append(data, type3Message(4, payload)...)

	conn := newReadConn(data)

	for _, ts := range []int64{20, 40, 60} {
		err := conn.pollMsg()
		require.NoError(t, err)
		require.Equal(t, uint8(msgtypeidAudioMsg), conn.msgtypeid)
		require.Equal(t, ts, conn.timestamp)
		require.Equal(t, payload, conn.msgdata)
	}
}

func TestReadChunkType3AfterType0ExtendedTimestamp(t *testing.T) {
	payload := append([]byte{0x2e}, bytes.Repeat([]byte{0xCC}, 99)...)

	data := chunkMessageExt(4, 0x01000000, msgtypeidAudioMsg, 1, payload, 128, false)
	// the type 3 chunk header has the extended timestamp with the delta
	data = append(data, 0xc4, 0x01, 0x00, 0x00, 0x00)
	data = append(data, payload...)

	conn := newReadConn(data)

	err := conn.pollMsg()
	require.NoError(t, err)
	require.Equal(t, int64(0x01000000), conn.timestamp)

	err = conn.pollMsg()
	require.NoError(t, err)
	require.Equal(t, int64(0x02000000), conn.timestamp)
	require.Equal(t, payload, conn.msgdata)
}

func TestReadChunkType3AfterType2(t *testing.T) {
	// the spec's example 1: type 0, type 2 with the delta, then type 3
	payload := append([]byte{0x2e}, bytes.Repeat([]byte{0xCC}, 99)...)

	data := chunkMessage(4, 1000, msgtypeidAudioMsg, 1, payload, 128)
	data = append(data, 0x84, 0x00, 0x00, 0x21) // type 2, delta 33
	data = append(data, payload...)
	data = append(data, type3Message(4, payload)...)
	data = append(data, type3Message(4, payload)...)

	conn := newReadConn(data)

	for _, ts := range []int64{1000, 1033, 1066, 1099} {
		err := conn.pollMsg()
		require.NoError(t, err)
		require.Equal(t, ts, conn.timestamp)
		require.Equal(t, payload, conn.msgdata)
	}
}

func TestTimestampDelta(t *testing.T) {
	for _, tc := range []struct {
		prev, now uint32
		delta     int64
	}{
		{0, 40, 40},
		{100, 90, -10},                    // small step backward
		{90, 133, 43},                     // small step forward
		{0, 2147479920, 2147479920},       // big step forward (sequence headers at 0)
		{0x7FFFFFF0, 0x10, 0x20},          // wraparound at 2^31 (e.g. FFmpeg)
		{0x7FFFFFF0, 0x80000010, 0x20},    // no wraparound at 2^31
		{0x80000010, 0x7FFFFFF0, -0x20},   // backward over 2^31
		{0xFFFFFFF0, 0x10, 0x20},          // wraparound at 2^32
		{0x2000000, 0x1000001, -0xFFFFFF}, // the biggest step backward
		{0x2000000, 0x1000000, 0},         // discontinuity
		{1000000040, 20, 0},               // discontinuity (restarted timestamps)
		{0x60000000, 0x10, 0},             // discontinuity, not a wraparound at 2^31
		{0xC0000000, 0x10, 0},             // discontinuity, not a wraparound at 2^32
	} {
		require.Equal(t, tc.delta, timestampDelta(tc.prev, tc.now), "prev %#x now %#x", tc.prev, tc.now)
	}
}

// videoMessages returns video messages with type 0 chunk headers with the given timestamps on the same chunk stream.
func videoMessages(timestamps ...uint32) []byte {
	payload := []byte{0x17, 0x01, 0x00, 0x00, 0x00, 0xAA}
	data := []byte{}
	for _, ts := range timestamps {
		if ts >= maxFlvTimestamp {
			data = append(data, chunkMessageExt(6, ts, msgtypeidVideoMsg, 1, payload, 128, false)...)
		} else {
			data = append(data, chunkMessage(6, ts, msgtypeidVideoMsg, 1, payload, 128)...)
		}
	}
	return data
}

func TestReadChunkTimestamps(t *testing.T) {
	for _, tc := range []struct {
		name       string
		timestamps []uint32
		expected   []int64
	}{
		{"non-monotonic", []uint32{100, 90, 133}, []int64{100, 90, 133}},
		{"wraparound at 2^31", []uint32{0x7FFFFFF0, 0x10, 0x30}, []int64{0x7FFFFFF0, 0x80000010, 0x80000030}},
		{"wraparound at 2^32", []uint32{0xFFFFFFF0, 0x10, 0x30}, []int64{0xFFFFFFF0, 0x100000010, 0x100000030}},
		{"large first timestamp", []uint32{0x50000000, 0x50000028}, []int64{0x50000000, 0x50000028}},
		{"sequence headers at 0 and wraparound at 2^31 (FFmpeg)", []uint32{0, 2147479920, 2147483640, 32}, []int64{0, 2147479920, 2147483640, 2147483680}},
		{"discontinuity", []uint32{1000000000, 1000000040, 20, 60}, []int64{1000000000, 1000000040, 1000000040, 1000000080}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			conn := newReadConn(videoMessages(tc.timestamps...))

			for i, ts := range tc.expected {
				err := conn.pollMsg()
				require.NoError(t, err, "message %d", i)
				require.Equal(t, ts, conn.timestamp, "message %d", i)
			}
		})
	}
}

// avMessage returns an audio (AAC raw) or video (AVC NALU) message with a type 0 chunk header.
func avMessage(msgtypeid uint8, timestamp uint32) []byte {
	if msgtypeid == msgtypeidAudioMsg {
		return chunkMessage(4, timestamp, msgtypeidAudioMsg, 1, []byte{0xAF, 0x01, 0xAA}, 128)
	}
	return chunkMessage(6, timestamp, msgtypeidVideoMsg, 1, []byte{0x17, 0x01, 0x00, 0x00, 0x00, 0xAA}, 128)
}

type avTimestamp struct {
	msgtypeid uint8
	timestamp int64
}

// readAVTimestamps reads count messages and returns their types and timestamps.
func readAVTimestamps(t *testing.T, conn *Conn, count int) []avTimestamp {
	result := []avTimestamp{}
	for i := 0; i < count; i++ {
		err := conn.pollMsg()
		require.NoError(t, err, "message %d", i)
		result = append(result, avTimestamp{conn.msgtypeid, conn.timestamp})
	}
	return result
}

func TestWallclockTimeConstantTimestamps(t *testing.T) {
	// video with increasing timestamps, audio with a constant timestamp
	data := []byte{}
	for i := 0; i < 15; i++ {
		data = append(data, avMessage(msgtypeidVideoMsg, uint32(40*i))...)
		data = append(data, avMessage(msgtypeidAudioMsg, 5000)...)
	}

	conn := newReadConn(data)
	timestamps := readAVTimestamps(t, conn, 30)

	require.True(t, conn.genwallclocktime)

	// the 12th audio message has the 11th equal timestamp in a row and switches to wall clock time
	switched := 2*11 + 1
	for i, ts := range timestamps[:switched] {
		if ts.msgtypeid == msgtypeidVideoMsg {
			require.Equal(t, int64(40*(i/2)), ts.timestamp, "message %d", i)
		} else {
			require.Equal(t, int64(5000), ts.timestamp, "message %d", i)
		}
	}

	// audio and video continue from the latest timestamp
	prev := int64(5000)
	for i, ts := range timestamps[switched:] {
		require.GreaterOrEqual(t, ts.timestamp, prev, "message %d", switched+i)
		require.Less(t, ts.timestamp, int64(5000+1000), "message %d", switched+i)
		prev = ts.timestamp
	}
}

func TestWallclockTimeThreshold(t *testing.T) {
	// 11 messages with the same timestamp are 10 equal timestamps in a row
	data := []byte{}
	for i := 0; i < 11; i++ {
		data = append(data, avMessage(msgtypeidVideoMsg, 1000)...)
	}
	data = append(data, avMessage(msgtypeidVideoMsg, 1040)...)

	conn := newReadConn(data)
	timestamps := readAVTimestamps(t, conn, 12)

	require.False(t, conn.genwallclocktime)
	require.Equal(t, int64(1040), timestamps[11].timestamp)

	// one more is the 11th equal timestamp in a row
	data = []byte{}
	for i := 0; i < 12; i++ {
		data = append(data, avMessage(msgtypeidVideoMsg, 1000)...)
	}

	conn = newReadConn(data)
	readAVTimestamps(t, conn, 12)

	require.True(t, conn.genwallclocktime)
}

func TestWallclockTimeStepsBackward(t *testing.T) {
	// steps backward are not equal timestamps
	data := []byte{}
	for i := 0; i < 15; i++ {
		data = append(data, avMessage(msgtypeidVideoMsg, uint32(1000-10*i))...)
	}

	conn := newReadConn(data)
	timestamps := readAVTimestamps(t, conn, 15)

	require.False(t, conn.genwallclocktime)
	for i, ts := range timestamps {
		require.Equal(t, int64(1000-10*i), ts.timestamp, "message %d", i)
	}
}

func TestWallclockTimeDisconnect(t *testing.T) {
	// after the first 20 messages, equal timestamps close the connection
	data := []byte{}
	for i := 0; i < 20; i++ {
		data = append(data, avMessage(msgtypeidVideoMsg, uint32(40*i))...)
	}
	// the first one is a step forward, followed by 11 equal timestamps in a row
	for i := 0; i < 12; i++ {
		data = append(data, avMessage(msgtypeidVideoMsg, 800)...)
	}

	conn := newReadConn(data)
	readAVTimestamps(t, conn, 31)

	err := conn.pollMsg()
	require.ErrorContains(t, err, "non-changing timestamps")
	require.False(t, conn.genwallclocktime)
}

func TestWriteAVTagTimestamp32Bit(t *testing.T) {
	tag := flvio.Tag{
		Type:          flvio.TAG_VIDEO,
		FrameType:     flvio.FRAME_KEY,
		CodecID:       flvio.VIDEO_H264,
		AVCPacketType: flvio.AVC_NALU,
		Data:          []byte{1, 2, 3},
	}

	for _, tc := range []struct {
		ts       int64
		expected uint32
	}{
		{0x7FFFFFFF, 0x7FFFFFFF},
		{0x80000000, 0x80000000}, // no wraparound at 2^31
		{0xFFFFFFFF, 0xFFFFFFFF},
		{0x100000027, 0x27}, // wraparound at 2^32
	} {
		conn, buf := newWriteConn()
		conn.avmsgsid = 1

		err := conn.writeAVTag(tag, tc.ts)
		require.NoError(t, err, "ts %#x", tc.ts)

		b := buf.Bytes()
		if tc.expected >= maxFlvTimestamp {
			require.Equal(t, maxFlvTimestamp, pio.U24BE(b[1:]), "ts %#x", tc.ts)
			require.Equal(t, tc.expected, pio.U32BE(b[12:]), "ts %#x", tc.ts)
		} else {
			require.Equal(t, tc.expected, pio.U24BE(b[1:]), "ts %#x", tc.ts)
		}
	}
}

// authorizeTest runs a server with the given authorization and a client that publishes or plays the url.
// It returns the error of the client, whether the publish or play handler has been called, and the error
// of the server.
func authorizeTest(t *testing.T, authorize func(conn *Conn) error, url string, publish bool) (error, bool, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer ln.Close()

	handled := make(chan bool, 1)

	server := &Server{
		HandleAuthorize: authorize,
		HandlePublish:   func(conn *Conn) { handled <- true },
		HandlePlay:      func(conn *Conn) { handled <- true },
	}

	result := make(chan error, 1)

	go func() {
		c, err := ln.Accept()
		if err != nil {
			result <- err
			return
		}
		result <- server.HandleNetConn(c)
	}()

	conn, err := Dial("rtmp://"+ln.Addr().String()+url, DialOptions{})
	require.NoError(t, err)
	defer conn.Close()

	flags := prepareWriting
	if !publish {
		flags = prepareReading
	}
	clienterr := conn.prepare(stageCommandDone, flags)

	err = <-result

	select {
	case <-handled:
		return clienterr, true, err
	default:
		return clienterr, false, err
	}
}

func TestAuthorizePublish(t *testing.T) {
	authorize := func(conn *Conn) error {
		if !conn.IsPublishing() || conn.IsPlaying() {
			return fmt.Errorf("not publishing")
		}
		if conn.URL.Path != "/live/test" {
			return fmt.Errorf("wrong path: %s", conn.URL.Path)
		}
		if conn.URL.Query().Get("token") != "secret" {
			return fmt.Errorf("invalid token")
		}
		return nil
	}

	clienterr, handled, err := authorizeTest(t, authorize, "/live/test?token=secret", true)
	require.NoError(t, clienterr)
	require.NoError(t, err)
	require.True(t, handled)

	clienterr, handled, err = authorizeTest(t, authorize, "/live/test?token=wrong", true)
	require.EqualError(t, clienterr, "command publish failed: NetStream.Publish.BadName (invalid token)")
	require.ErrorContains(t, err, "publish not authorized: invalid token")
	require.False(t, handled)
}

func TestAuthorizePlay(t *testing.T) {
	authorize := func(conn *Conn) error {
		if !conn.IsPlaying() || conn.IsPublishing() {
			return fmt.Errorf("not playing")
		}
		if conn.URL.Path != "/live/test" {
			return fmt.Errorf("wrong path: %s", conn.URL.Path)
		}
		if conn.URL.Query().Get("token") != "secret" {
			return fmt.Errorf("invalid token")
		}
		return nil
	}

	clienterr, handled, err := authorizeTest(t, authorize, "/live/test?token=secret", false)
	require.NoError(t, clienterr)
	require.NoError(t, err)
	require.True(t, handled)

	clienterr, handled, err = authorizeTest(t, authorize, "/live/test?token=wrong", false)
	require.EqualError(t, clienterr, "command play failed: NetStream.Play.StreamNotFound (invalid token)")
	require.ErrorContains(t, err, "play not authorized: invalid token")
	require.False(t, handled)
}

// commandMessage returns an AMF0 command message on chunk stream 3.
func commandMessage(args ...interface{}) []byte {
	size := 0
	for _, arg := range args {
		size += flvio.LenAMF0Val(arg)
	}
	payload := make([]byte, size)
	n := 0
	for _, arg := range args {
		n += flvio.FillAMF0Val(payload[n:], arg)
	}
	return chunkMessage(3, 0, msgtypeidCommandMsgAMF0, 0, payload, 128)
}

func TestConnectObjectEncoding(t *testing.T) {
	for _, tc := range []struct {
		name     string
		encoding interface{}
	}{
		{"none", nil},
		{"AMF0", 0.0},
		{"AMF3", 3.0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			params := flvio.AMFMap{
				"app":   "live",
				"tcUrl": "rtmp://localhost/live",
			}
			if tc.encoding != nil {
				params["objectEncoding"] = tc.encoding
			}

			conn := newReadConn(commandMessage("connect", 1.0, params))
			buf := &bytes.Buffer{}
			conn.bufw = bufio.NewWriter(buf)
			conn.isserver = true

			// fails at the end of the data, after the answer to connect
			err := conn.readConnect()
			require.ErrorIs(t, err, io.EOF)

			// the answer of the server
			rconn := newReadConn(buf.Bytes())
			rconn.readMaxChunkSize = 65536

			var info flvio.AMFMap
			for info == nil {
				err = rconn.pollMsg()
				require.NoError(t, err)
				if rconn.gotcommand && rconn.commandname == "_result" && len(rconn.commandparams) > 0 {
					info, _ = rconn.commandparams[0].(flvio.AMFMap)
				}
			}

			require.Equal(t, "NetConnection.Connect.Success", info["code"])
			require.Equal(t, 0.0, info["objectEncoding"])
		})
	}
}

type commandAnswer struct {
	name    string
	transid float64
	params  []interface{}
}

// commandsAnswer feeds the server side with the commands and returns the commands it answers with.
func commandsAnswer(t *testing.T, commands ...[]byte) []commandAnswer {
	data := commandMessage("connect", 1.0, flvio.AMFMap{"app": "live", "tcUrl": "rtmp://localhost/live"})
	for _, c := range commands {
		data = append(data, c...)
	}

	conn := newReadConn(data)
	buf := &bytes.Buffer{}
	conn.bufw = bufio.NewWriter(buf)
	conn.isserver = true

	err := conn.readConnect()
	if err != nil {
		require.ErrorIs(t, err, io.EOF)
	}
	conn.flushWrite()

	// the answers of the server
	rconn := newReadConn(buf.Bytes())
	rconn.readMaxChunkSize = 65536

	answers := []commandAnswer{}
	for {
		if err := rconn.pollMsg(); err != nil {
			break
		}
		if rconn.gotcommand {
			answers = append(answers, commandAnswer{rconn.commandname, rconn.commandtransid, rconn.commandparams})
		}
	}

	return answers
}

func TestCommandsFMLEPublish(t *testing.T) {
	// the commands of FFmpeg and OBS before publishing
	answers := commandsAnswer(t,
		commandMessage("releaseStream", 2.0, nil, "test"),
		commandMessage("FCPublish", 3.0, nil, "test"),
		commandMessage("createStream", 4.0, nil),
		commandMessage("publish", 5.0, nil, "test", "live"),
	)

	names := []string{}
	for _, a := range answers {
		names = append(names, a.name)
	}
	require.Equal(t, []string{"_result", "onBWDone", "_result", "_result", "_result", "onStatus"}, names)

	// connect, releaseStream, FCPublish, createStream
	require.Equal(t, 1.0, answers[0].transid)
	require.Equal(t, 2.0, answers[2].transid)
	require.Equal(t, 3.0, answers[3].transid)
	require.Equal(t, 4.0, answers[4].transid)
	require.Equal(t, []interface{}{1.0}, answers[4].params)

	// onStatus has transaction id 0
	require.Equal(t, 0.0, answers[5].transid)
	info, _ := answers[5].params[0].(flvio.AMFMap)
	require.Equal(t, "NetStream.Publish.Start", info["code"])
}

func TestCommandsWithoutAnswer(t *testing.T) {
	// commands with transaction id 0 don't expect an answer
	answers := commandsAnswer(t,
		commandMessage("releaseStream", 0.0, nil, "test"),
		commandMessage("FCPublish", 0.0, nil, "test"),
		commandMessage("createStream", 2.0, nil),
		commandMessage("play", 0.0, nil, "test"),
	)

	names := []string{}
	for _, a := range answers {
		names = append(names, a.name)
	}
	require.Equal(t, []string{"_result", "onBWDone", "_result", "onStatus"}, names)

	require.Equal(t, 0.0, answers[3].transid)
	info, _ := answers[3].params[0].(flvio.AMFMap)
	require.Equal(t, "NetStream.Play.Start", info["code"])
}

// dataMessage returns an AMF0 data message on chunk stream 4.
func dataMessage(args ...interface{}) []byte {
	size := 0
	for _, arg := range args {
		size += flvio.LenAMF0Val(arg)
	}
	payload := make([]byte, size)
	n := 0
	for _, arg := range args {
		n += flvio.FillAMF0Val(payload[n:], arg)
	}
	return chunkMessage(4, 0, msgtypeidDataMsgAMF0, 1, payload, 128)
}

// publishData returns the commands of a publisher and an AAC stream with the given number of frames.
func publishData(frames int) []byte {
	data := commandMessage("connect", 1.0, flvio.AMFMap{"app": "live", "tcUrl": "rtmp://localhost/live"})
	data = append(data, commandMessage("createStream", 2.0, nil)...)
	data = append(data, commandMessage("publish", 3.0, nil, "test", "live")...)
	data = append(data, dataMessage("@setDataFrame", "onMetaData", flvio.AMFMap{"audiocodecid": 10.0})...)
	// AAC sequence header: AAC LC, 44.1 kHz, stereo
	data = append(data, chunkMessage(4, 0, msgtypeidAudioMsg, 1, []byte{0xAF, 0x00, 0x12, 0x10}, 128)...)
	for i := 0; i < frames; i++ {
		data = append(data, chunkMessage(4, uint32(23*i), msgtypeidAudioMsg, 1, []byte{0xAF, 0x01, byte(i)}, 128)...)
	}
	return data
}

func TestPublisherEndsStream(t *testing.T) {
	for _, command := range []string{"FCUnpublish", "deleteStream", "closeStream"} {
		t.Run(command, func(t *testing.T) {
			data := publishData(3)
			if command == "deleteStream" {
				data = append(data, commandMessage(command, 6.0, nil, 1.0)...)
			} else {
				data = append(data, commandMessage(command, 6.0, nil, "test")...)
			}
			// must not be read anymore
			data = append(data, chunkMessage(4, 1000, msgtypeidAudioMsg, 1, []byte{0xAF, 0x01, 0xFF}, 128)...)

			conn := newReadConn(data)
			conn.isserver = true
			conn.stage = stageHandshakeDone

			for i := 0; i < 3; i++ {
				pkt, err := conn.ReadPacket()
				require.NoError(t, err, "packet %d", i)
				require.Equal(t, []byte{byte(i)}, pkt.Data, "packet %d", i)
			}

			_, err := conn.ReadPacket()
			require.ErrorIs(t, err, io.EOF)

			// the end of the stream stays
			_, err = conn.ReadPacket()
			require.ErrorIs(t, err, io.EOF)
		})
	}
}

func TestPublisherEndsStreamTCP(t *testing.T) {
	// without a command, the stream ends with the connection
	conn := newReadConn(publishData(3))
	conn.isserver = true
	conn.stage = stageHandshakeDone

	for i := 0; i < 3; i++ {
		_, err := conn.ReadPacket()
		require.NoError(t, err, "packet %d", i)
	}

	_, err := conn.ReadPacket()
	require.ErrorIs(t, err, io.EOF)
}

// newScriptedClient returns a client connection after the handshake that reads the given server messages.
func newScriptedClient(t *testing.T, data []byte) *Conn {
	conn := newReadConn(data)
	conn.stage = stageHandshakeDone
	u, err := ParseURL("rtmp://localhost/live/test")
	require.NoError(t, err)
	conn.URL = u
	return conn
}

func statusMessage(name string, transid float64, level, code, description string) []byte {
	return commandMessage(name, transid, nil, flvio.AMFMap{"level": level, "code": code, "description": description})
}

var (
	connectResult      = commandMessage("_result", 1.0, flvio.AMFMap{"fmsVer": "FMS/3,0,1,123"}, flvio.AMFMap{"level": "status", "code": "NetConnection.Connect.Success"})
	createStreamResult = commandMessage("_result", 2.0, nil, 1.0)
)

func concat(parts ...[]byte) []byte {
	data := []byte{}
	for _, p := range parts {
		data = append(data, p...)
	}
	return data
}

func TestClientCommandErrors(t *testing.T) {
	for _, tc := range []struct {
		name    string
		publish bool
		data    []byte
		err     string
	}{
		{"connect _error", true, concat(
			statusMessage("_error", 1.0, "error", "NetConnection.Connect.Rejected", "not allowed"),
		), "command connect failed: NetConnection.Connect.Rejected (not allowed)"},
		{"createStream _error", true, concat(
			connectResult,
			statusMessage("_error", 2.0, "error", "NetConnection.Call.Failed", ""),
		), "command createStream failed: NetConnection.Call.Failed"},
		{"publish rejected", true, concat(
			connectResult, createStreamResult,
			statusMessage("onStatus", 0, "error", "NetStream.Publish.BadName", "invalid token"),
		), "command publish failed: NetStream.Publish.BadName (invalid token)"},
		{"publish _error", true, concat(
			connectResult, createStreamResult,
			statusMessage("_error", 3.0, "error", "NetConnection.Call.Failed", "no publish"),
		), "command publish failed: NetConnection.Call.Failed (no publish)"},
		{"play not found", false, concat(
			connectResult, createStreamResult,
			statusMessage("onStatus", 0, "status", "NetStream.Play.Reset", ""),
			statusMessage("onStatus", 0, "error", "NetStream.Play.StreamNotFound", "no stream"),
		), "command play failed: NetStream.Play.StreamNotFound (no stream)"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			conn := newScriptedClient(t, tc.data)

			flags := prepareWriting
			if !tc.publish {
				flags = prepareReading
			}

			err := conn.prepare(stageCommandDone, flags)
			require.EqualError(t, err, tc.err)
		})
	}
}

func TestClientCommandResults(t *testing.T) {
	// answers with other transaction ids and other messages are skipped
	data := concat(
		commandMessage("onBWDone", 0.0, nil, 8192.0),
		commandMessage("_result", 7.0, nil),
		connectResult,
		commandMessage("_error", 7.0, nil, flvio.AMFMap{"code": "NetConnection.Call.Failed"}),
		createStreamResult,
		statusMessage("onStatus", 0, "status", "NetStream.Publish.Start", "Start publishing"),
	)

	conn := newScriptedClient(t, data)

	err := conn.prepare(stageCommandDone, prepareWriting)
	require.NoError(t, err)
	require.True(t, conn.IsPublishing())
	require.Equal(t, uint32(1), conn.avmsgsid)
}

// playData returns the answers of a server to a player and an AAC stream with the given number of frames.
func playData(frames int) []byte {
	data := concat(
		connectResult, createStreamResult,
		statusMessage("onStatus", 0, "status", "NetStream.Play.Reset", ""),
		statusMessage("onStatus", 0, "status", "NetStream.Play.Start", ""),
		dataMessage("onMetaData", flvio.AMFMap{"audiocodecid": 10.0}),
		chunkMessage(4, 0, msgtypeidAudioMsg, 1, []byte{0xAF, 0x00, 0x12, 0x10}, 128),
	)
	for i := 0; i < frames; i++ {
		data = append(data, chunkMessage(4, uint32(23*i), msgtypeidAudioMsg, 1, []byte{0xAF, 0x01, byte(i)}, 128)...)
	}
	return data
}

func TestClientPlayEnds(t *testing.T) {
	for _, code := range []string{"NetStream.Play.UnpublishNotify", "NetStream.Play.Stop", "NetStream.Play.Complete"} {
		t.Run(code, func(t *testing.T) {
			data := playData(3)
			data = append(data, statusMessage("onStatus", 0, "status", code, "")...)
			// must not be read anymore
			data = append(data, chunkMessage(4, 1000, msgtypeidAudioMsg, 1, []byte{0xAF, 0x01, 0xFF}, 128)...)

			conn := newScriptedClient(t, data)

			for i := 0; i < 3; i++ {
				pkt, err := conn.ReadPacket()
				require.NoError(t, err, "packet %d", i)
				require.Equal(t, []byte{byte(i)}, pkt.Data, "packet %d", i)
			}

			_, err := conn.ReadPacket()
			require.ErrorIs(t, err, io.EOF)

			_, err = conn.ReadPacket()
			require.ErrorIs(t, err, io.EOF)
		})
	}
}

func TestClientPlayError(t *testing.T) {
	data := playData(2)
	data = append(data, statusMessage("onStatus", 0, "error", "NetStream.Play.Failed", "gone")...)

	conn := newScriptedClient(t, data)

	for i := 0; i < 2; i++ {
		_, err := conn.ReadPacket()
		require.NoError(t, err, "packet %d", i)
	}

	_, err := conn.ReadPacket()
	require.EqualError(t, err, "play failed: NetStream.Play.Failed (gone)")
}

func TestClientPlayOtherStatus(t *testing.T) {
	// other status messages during playing are skipped
	data := playData(1)
	data = append(data, statusMessage("onStatus", 0, "status", "NetStream.Play.PublishNotify", "")...)
	data = append(data, chunkMessage(4, 1000, msgtypeidAudioMsg, 1, []byte{0xAF, 0x01, 0x01}, 128)...)

	conn := newScriptedClient(t, data)

	for i := 0; i < 2; i++ {
		pkt, err := conn.ReadPacket()
		require.NoError(t, err, "packet %d", i)
		require.Equal(t, []byte{byte(i)}, pkt.Data, "packet %d", i)
	}
}
