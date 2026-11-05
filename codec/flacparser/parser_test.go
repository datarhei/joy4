package flacparser

import (
	"encoding/hex"
	"testing"
	"time"

	"github.com/datarhei/joy4/av"
	"github.com/stretchr/testify/require"
)

// STREAMINFO as FFmpeg writes it (without the metadata block header): 48 kHz mono 16 bit, at the start and
// at the end of the stream (with the total samples, frame sizes and MD5), 48 kHz 5.1 16 bit and 44.1 kHz
// mono 24 bit
const (
	streamInfoMono      = "040004000000000008150bb800f00000000000000000000000000000000000000000"
	streamInfoMonoFinal = "040004000000fc00018b0bb800f0000232800e9d7dce5ba2b3c21e0da827578eebfe"
	streamInfo51        = "040004000000000030240bb80af00000000000000000000000000000000000000000"
	streamInfo24bit     = "04000400000000000c160ac441700000000000000000000000000000000000000000"
)

func decodeHex(t *testing.T, s string) []byte {
	b, err := hex.DecodeString(s)
	require.NoError(t, err)
	return b
}

func TestNewCodecDataFromStreamInfo(t *testing.T) {
	for _, tc := range []struct {
		name          string
		streamInfo    string
		sampleRate    int
		channels      int
		bitsPerSample int
		totalSamples  uint64
		maxFrameSize  int
		layout        av.ChannelLayout
		format        av.SampleFormat
	}{
		{"mono", streamInfoMono, 48000, 1, 16, 0, 0x815, av.CH_MONO, av.S16},
		{"mono final", streamInfoMonoFinal, 48000, 1, 16, 144000, 0x18b, av.CH_MONO, av.S16},
		{"5.1", streamInfo51, 48000, 6, 16, 0, 0x3024, av.CH_SURROUND | av.CH_LOW_FREQ | av.CH_BACK_LEFT | av.CH_BACK_RIGHT, av.S16},
		{"24 bit", streamInfo24bit, 44100, 1, 24, 0, 0xc16, av.CH_MONO, av.S32},
	} {
		b := decodeHex(t, tc.streamInfo)
		// the bare STREAMINFO (FFmpeg), and with the "fLaC" marker and the metadata block header (E-RTMP v2)
		for _, header := range [][]byte{b, append(decodeHex(t, "664c614380000022"), b...)} {
			codec, err := NewCodecDataFromSequenceHeader(header)
			require.NoError(t, err, tc.name)
			require.Equal(t, av.FLAC, codec.Type(), tc.name)
			require.Equal(t, tc.sampleRate, codec.SampleRate(), tc.name)
			require.Equal(t, tc.channels, codec.Channels, tc.name)
			require.Equal(t, tc.bitsPerSample, codec.BitsPerSample, tc.name)
			require.Equal(t, tc.totalSamples, codec.TotalSamples, tc.name)
			require.Equal(t, 1024, codec.MinBlockSize, tc.name)
			require.Equal(t, 1024, codec.MaxBlockSize, tc.name)
			require.Equal(t, tc.maxFrameSize, codec.MaxFrameSize, tc.name)
			require.Equal(t, tc.layout, codec.ChannelLayout(), tc.name)
			require.Equal(t, tc.channels, codec.ChannelLayout().Count(), tc.name)
			require.Equal(t, tc.format, codec.SampleFormat(), tc.name)
			require.Equal(t, b, codec.StreamInfoBytes(), tc.name)
			// the sequence header is kept as it was received
			require.Equal(t, header, codec.SequenceHeaderBytes(), tc.name)
		}

		// without one, it is the "fLaC" marker and the STREAMINFO metadata block
		codec, err := NewCodecDataFromStreamInfo(b)
		require.NoError(t, err, tc.name)
		require.Nil(t, codec.Header, tc.name)
		require.Equal(t, append(decodeHex(t, "664c614380000022"), b...), codec.SequenceHeaderBytes(), tc.name)
	}

	// further metadata blocks after STREAMINFO are ignored
	header := append(decodeHex(t, "664c614300000022"), decodeHex(t, streamInfoMono)...)
	header = append(header, 0x84, 0x00, 0x00, 0x01, 0x00)
	codec, err := NewCodecDataFromSequenceHeader(header)
	require.NoError(t, err)
	require.Equal(t, decodeHex(t, streamInfoMono), codec.StreamInfo)
	require.Equal(t, header, codec.SequenceHeaderBytes())

	// the codec data doesn't alias the sequence header
	b := decodeHex(t, streamInfoMono)
	codec, err = NewCodecDataFromSequenceHeader(b)
	require.NoError(t, err)
	b[0] = 0xff
	require.Equal(t, decodeHex(t, streamInfoMono), codec.StreamInfoBytes())
	require.Equal(t, decodeHex(t, streamInfoMono), codec.SequenceHeaderBytes())
}

func TestNewCodecDataFromSequenceHeaderInvalid(t *testing.T) {
	streamInfo := decodeHex(t, streamInfoMono)
	marker := decodeHex(t, "664c614380000022")

	// every truncation
	full := append(append([]byte(nil), marker...), streamInfo...)
	for n := 0; n < len(full); n++ {
		_, err := NewCodecDataFromSequenceHeader(full[:n])
		if n == StreamInfoSize {
			// 34 bytes are taken as the bare STREAMINFO, which these are not
			continue
		}
		require.Error(t, err, "%d bytes", n)
	}

	for _, tc := range []struct {
		name   string
		header []byte
	}{
		{"no marker", append(decodeHex(t, "664c616480000022"), streamInfo...)},
		{"not STREAMINFO first", append(decodeHex(t, "664c614384000022"), streamInfo...)},
		{"wrong STREAMINFO size", append(decodeHex(t, "664c614380000021"), streamInfo...)},
		{"no sample rate", modified(streamInfo, 10, 0x00, 0x00, 0x00)},
		{"block size too small", modified(streamInfo, 0, 0x00, 0x0f, 0x00, 0x0f)},
		{"max block size too small", modified(streamInfo, 2, 0x03, 0xff)},
	} {
		if tc.header[0] != 'f' {
			// the bare STREAMINFO
			require.Len(t, tc.header, StreamInfoSize, tc.name)
		}
		_, err := NewCodecDataFromSequenceHeader(tc.header)
		require.Error(t, err, tc.name)
	}

	_, err := NewCodecDataFromStreamInfo(streamInfo[:33])
	require.Error(t, err)
}

// modified returns a copy of b with the bytes from pos replaced.
func modified(b []byte, pos int, bytes ...byte) []byte {
	b = append([]byte(nil), b...)
	copy(b[pos:], bytes)
	return b
}

func TestChannelLayout(t *testing.T) {
	for channels := 1; channels <= 8; channels++ {
		layout, err := ChannelLayout(channels)
		require.NoError(t, err)
		require.Equal(t, channels, layout.Count())
	}
	_, err := ChannelLayout(0)
	require.Error(t, err)
	_, err = ChannelLayout(9)
	require.Error(t, err)
}

func TestFrameBlockSize(t *testing.T) {
	for _, tc := range []struct {
		name      string
		header    string
		blockSize int
	}{
		// FFmpeg: 1024 samples (code 10), and the last frame with 640 samples (code 7 after a 2 byte number)
		{"ffmpeg", "fff8aa0800ed4c000000eb", 1024},
		{"ffmpeg last frame", "fff87a08c28c027f7c4c0b", 640},
		{"variable block size", "fff9aa0800ed4c", 1024},
		{"192", "fff8180800", 192},
		{"576", "fff8280800", 576},
		{"4608", "fff8580800", 4608},
		{"256", "fff8880800", 256},
		{"32768", "fff8f80800", 32768},
		{"8 bit", "fff8680800ff", 256},
		{"8 bit after a 1 byte number", "fff868087f00", 1},
		{"16 bit after a 3 byte number", "fff87808e080800fff", 4096},
		{"16 bit after a 7 byte number", "fff87808fe8080808080800000", 1},
	} {
		blockSize, err := FrameBlockSize(decodeHex(t, tc.header))
		require.NoError(t, err, tc.name)
		require.Equal(t, tc.blockSize, blockSize, tc.name)
	}

	for _, tc := range []struct {
		name   string
		header string
	}{
		{"empty", ""},
		{"too short", "fff8aa08"},
		{"no sync code", "fff0aa0800"},
		{"no sync code 2", "7ff8aa0800"},
		{"reserved block size", "fff80a0800"},
		{"continuation byte as number", "fff8680880ff"},
		{"8 byte number", "fff86808ff808080808080800000"},
		{"8 bit missing", "fff8680800"},
		{"16 bit missing", "fff878080000"},
		{"16 bit missing after a 2 byte number", "fff87808c28c02"},
	} {
		_, err := FrameBlockSize(decodeHex(t, tc.header))
		require.Error(t, err, tc.name)
	}
}

func TestPacketDuration(t *testing.T) {
	codec, err := NewCodecDataFromStreamInfo(decodeHex(t, streamInfoMono))
	require.NoError(t, err)

	duration, err := codec.PacketDuration(decodeHex(t, "fff8aa0800ed4c000000eb"))
	require.NoError(t, err)
	require.Equal(t, 1024*time.Second/48000, duration)

	duration, err = codec.PacketDuration(decodeHex(t, "fff87a08c28c027f7c4c0b"))
	require.NoError(t, err)
	require.Equal(t, 640*time.Second/48000, duration)

	_, err = codec.PacketDuration(nil)
	require.Error(t, err)
}
