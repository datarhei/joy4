package opusparser

import (
	"encoding/hex"
	"testing"
	"time"

	"github.com/datarhei/joy4/av"
	"github.com/stretchr/testify/require"
)

func TestNewCodecDataFromOpusHead(t *testing.T) {
	// ID headers of FFmpeg (libopus)
	for _, tc := range []struct {
		header   string
		channels int
		layout   av.ChannelLayout
		streams  int
		coupled  int
		mapping  []uint8
	}{
		{"4f707573486561640101380180bb0000000000", 1, av.CH_MONO, 1, 0, nil},
		{"4f707573486561640102380180bb0000000000", 2, av.CH_STEREO, 1, 1, nil},
		{"4f707573486561640106380180bb00000000010402000401020305", 6, av.CH_SURROUND | av.CH_BACK_LEFT | av.CH_BACK_RIGHT | av.CH_LOW_FREQ, 4, 2, []uint8{0, 4, 1, 2, 3, 5}},
	} {
		header, err := hex.DecodeString(tc.header)
		require.NoError(t, err)
		codec, err := NewCodecDataFromOpusHead(header)
		require.NoError(t, err, tc.header)
		require.Equal(t, av.OPUS, codec.Type())
		require.Equal(t, 48000, codec.SampleRate())
		require.Equal(t, tc.channels, codec.Channels)
		require.Equal(t, tc.layout, codec.ChannelLayout())
		require.Equal(t, tc.channels, codec.ChannelLayout().Count())
		require.Equal(t, 312, codec.PreSkip)
		require.Equal(t, 48000, codec.InputSampleRate)
		require.Equal(t, tc.streams, codec.StreamCount)
		require.Equal(t, tc.coupled, codec.CoupledCount)
		require.Equal(t, tc.mapping, codec.ChannelMapping)
		require.Equal(t, header, codec.OpusHeadBytes())

		// every truncation is an error
		for i := 0; i < len(header); i++ {
			_, err := NewCodecDataFromOpusHead(header[:i])
			require.Error(t, err, "%s: %d bytes", tc.header, i)
		}
	}

	for name, header := range map[string]string{
		"magic":       "4f707573486561650101380180bb0000000000",
		"version":     "4f707573486561641101380180bb0000000000",
		"no channels": "4f707573486561640100380180bb0000000000",
		"family 0":    "4f707573486561640103380180bb0000000000",
		"no streams":  "4f707573486561640106380180bb00000000010002000401020305",
		"coupled":     "4f707573486561640106380180bb00000000010205000401020305",
		"9 channels":  "4f707573486561640109380180bb000000000105040004010203050607",
	} {
		b, err := hex.DecodeString(header)
		require.NoError(t, err)
		_, err = NewCodecDataFromOpusHead(b)
		require.Error(t, err, name)
	}
}

func TestNewCodecData(t *testing.T) {
	codec, err := NewCodecData(2)
	require.NoError(t, err)
	require.Equal(t, av.CH_STEREO, codec.ChannelLayout())
	require.Nil(t, codec.Header)

	// a minimal ID header is written for it
	header, err := hex.DecodeString("4f707573486561640102000080bb0000000000")
	require.NoError(t, err)
	require.Equal(t, header, codec.OpusHeadBytes())
	parsed, err := NewCodecDataFromOpusHead(codec.OpusHeadBytes())
	require.NoError(t, err)
	require.Equal(t, av.CH_STEREO, parsed.ChannelLayout())

	for _, channels := range []int{0, 3} {
		_, err := NewCodecData(channels)
		require.Error(t, err)
	}
}

func TestPacketDuration(t *testing.T) {
	for _, tc := range []struct {
		data     []byte
		duration time.Duration
	}{
		// CELT 20 ms, as FFmpeg encodes it, mono and stereo
		{[]byte{0xf8, 0x72}, 20 * time.Millisecond},
		{[]byte{0xfc, 0xfc}, 20 * time.Millisecond},
		// CELT 10 ms
		{[]byte{0xf4}, 10 * time.Millisecond},
		// CELT 2.5 ms
		{[]byte{0x80}, 2500 * time.Microsecond},
		// SILK 60 ms
		{[]byte{0x18}, 60 * time.Millisecond},
		// hybrid 10 ms, two frames (code 1 and 2)
		{[]byte{0x61}, 20 * time.Millisecond},
		{[]byte{0x62}, 20 * time.Millisecond},
		// CELT 20 ms, code 3 with 3 frames
		{[]byte{0xfb, 0x03}, 60 * time.Millisecond},
	} {
		duration, err := PacketDuration(tc.data)
		require.NoError(t, err, "%x", tc.data)
		require.Equal(t, tc.duration, duration, "%x", tc.data)
	}

	_, err := PacketDuration(nil)
	require.Error(t, err)
	_, err = PacketDuration([]byte{0xfb})
	require.Error(t, err)

	require.True(t, IsStereo([]byte{0xfc}))
	require.False(t, IsStereo([]byte{0xf8}))
	require.False(t, IsStereo(nil))
}
