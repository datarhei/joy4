package rtmp

import (
	"bufio"
	"bytes"
	"net"
	"testing"

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
