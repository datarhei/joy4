package mp3parser

import (
	"encoding/hex"
	"testing"
	"time"

	"github.com/datarhei/joy4/av"
	"github.com/stretchr/testify/require"
)

// the start of frames from FFmpeg (libmp3lame): MPEG-1 44.1 kHz and 48 kHz stereo, MPEG-2 22.05 kHz mono and
// MPEG-2.5 11.025 kHz mono
const (
	mpeg1Stereo44k = "fffb90640000026b0bce"
	mpeg1Stereo48k = "fffb9464000001ee0850"
	mpeg2Mono22k   = "fff340c400133062843f"
	mpeg25Mono11k  = "ffe320c400125002cdbf"
)

func decodeHex(t *testing.T, s string) []byte {
	b, err := hex.DecodeString(s)
	require.NoError(t, err)
	return b
}

// modified returns the frame with the byte at pos replaced.
func modified(t *testing.T, s string, pos int, b byte) []byte {
	data := decodeHex(t, s)
	data[pos] = b
	return data
}

func TestNewCodecDataFromFrame(t *testing.T) {
	for _, tc := range []struct {
		name       string
		frame      []byte
		version    int
		sampleRate int
		bitRate    int
		layout     av.ChannelLayout
		duration   time.Duration
	}{
		{"mpeg-1 44.1 kHz", decodeHex(t, mpeg1Stereo44k), MPEG1, 44100, 128, av.CH_STEREO, 1152 * time.Second / 44100},
		{"mpeg-1 48 kHz", decodeHex(t, mpeg1Stereo48k), MPEG1, 48000, 128, av.CH_STEREO, 24 * time.Millisecond},
		{"mpeg-2 22.05 kHz", decodeHex(t, mpeg2Mono22k), MPEG2, 22050, 32, av.CH_MONO, 576 * time.Second / 22050},
		{"mpeg-2.5 11.025 kHz", decodeHex(t, mpeg25Mono11k), MPEG25, 11025, 16, av.CH_MONO, 576 * time.Second / 11025},
		{"mpeg-1 32 kHz", modified(t, mpeg1Stereo44k, 2, 0x98), MPEG1, 32000, 128, av.CH_STEREO, 36 * time.Millisecond},
		{"mpeg-2 24 kHz", modified(t, mpeg2Mono22k, 2, 0x44), MPEG2, 24000, 32, av.CH_MONO, 24 * time.Millisecond},
		{"mpeg-2.5 8 kHz", modified(t, mpeg25Mono11k, 2, 0x28), MPEG25, 8000, 16, av.CH_MONO, 72 * time.Millisecond},
		{"free format", modified(t, mpeg1Stereo44k, 2, 0x00), MPEG1, 44100, 0, av.CH_STEREO, 1152 * time.Second / 44100},
		// joint stereo and dual channel
		{"joint stereo", modified(t, mpeg1Stereo44k, 3, 0x44), MPEG1, 44100, 128, av.CH_STEREO, 1152 * time.Second / 44100},
		{"dual channel", modified(t, mpeg1Stereo44k, 3, 0x84), MPEG1, 44100, 128, av.CH_STEREO, 1152 * time.Second / 44100},
	} {
		codec, err := NewCodecDataFromFrame(tc.frame)
		require.NoError(t, err, tc.name)
		require.Equal(t, av.MP3, codec.Type(), tc.name)
		require.Equal(t, tc.version, codec.Version, tc.name)
		require.Equal(t, tc.sampleRate, codec.SampleRate(), tc.name)
		require.Equal(t, tc.bitRate, codec.BitRate, tc.name)
		require.Equal(t, tc.layout, codec.ChannelLayout(), tc.name)
		require.Equal(t, av.FLTP, codec.SampleFormat(), tc.name)
		duration, err := codec.PacketDuration(tc.frame)
		require.NoError(t, err, tc.name)
		require.Equal(t, tc.duration, duration, tc.name)
	}
}

func TestNewCodecDataFromFrameInvalid(t *testing.T) {
	for _, tc := range []struct {
		name  string
		frame []byte
	}{
		{"empty", nil},
		{"too short", decodeHex(t, mpeg1Stereo44k)[:3]},
		{"no sync", modified(t, mpeg1Stereo44k, 0, 0xfe)},
		{"no sync 2", modified(t, mpeg1Stereo44k, 1, 0xdb)},
		{"reserved version", modified(t, mpeg1Stereo44k, 1, 0xeb)},
		{"layer II", modified(t, mpeg1Stereo44k, 1, 0xfd)},
		{"layer I", modified(t, mpeg1Stereo44k, 1, 0xff)},
		{"reserved layer", modified(t, mpeg1Stereo44k, 1, 0xf9)},
		{"invalid bit rate", modified(t, mpeg1Stereo44k, 2, 0xf0)},
		{"reserved sample rate", modified(t, mpeg1Stereo44k, 2, 0x9c)},
	} {
		_, err := NewCodecDataFromFrame(tc.frame)
		require.Error(t, err, tc.name)
	}

	codec, err := NewCodecDataFromFrame(decodeHex(t, mpeg1Stereo44k))
	require.NoError(t, err)
	_, err = codec.PacketDuration([]byte{0xff})
	require.Error(t, err)
}
