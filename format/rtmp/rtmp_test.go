package rtmp

import (
	"bufio"
	"bytes"
	"errors"
	"io"
	"net"
	"os"
	"testing"
	"time"

	"github.com/datarhei/joy4/format/flv/flvio"
	"github.com/datarhei/joy4/utils/bits/pio"
	"github.com/stretchr/testify/require"
)

// newReadConn returns a connection that reads the given data.
func newReadConn(data []byte) *Conn {
	c1, _ := net.Pipe()
	conn := NewConn(c1)
	conn.bufr = bufio.NewReader(bytes.NewReader(data))
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

	for _, ts := range []int64{0, 0xFFFFFE, 0xFFFFFF, 0x1000000, 0x7FFFFF00} {
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
