package ac3parser

import (
	"encoding/hex"
	"testing"
	"time"

	"github.com/datarhei/joy4/av"
	"github.com/stretchr/testify/require"
)

// the start of syncframes from FFmpeg's encoders
const (
	ac3Stereo48k  = "0b7761e4144043e106f4"
	ac3Surround51 = "0b772b9a1e40ebf8403e"
	ac3Mono44k    = "0b77b92f4c402f842be1"
	eac3Stereo48k = "0b77017f3487c0002084"
	eac3Surround  = "0b77037f3f87c00020fc"
	eac3Stereo32k = "0b77023fb487c0000080"
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
	surround51 := av.CH_SURROUND | av.CH_SIDE_LEFT | av.CH_SIDE_RIGHT | av.CH_LOW_FREQ

	for _, tc := range []struct {
		name       string
		frame      []byte
		typ        av.CodecType
		sampleRate int
		layout     av.ChannelLayout
		mode       int
		duration   time.Duration
	}{
		{"ac-3 stereo", decodeHex(t, ac3Stereo48k), av.AC3, 48000, av.CH_STEREO, 2, 32 * time.Millisecond},
		{"ac-3 5.1", decodeHex(t, ac3Surround51), av.AC3, 48000, surround51, 7, 32 * time.Millisecond},
		{"ac-3 mono 44.1 kHz", decodeHex(t, ac3Mono44k), av.AC3, 44100, av.CH_MONO, 1, 1536 * time.Second / 44100},
		// 1+1 (dual mono), without LFE
		{"ac-3 1+1", modified(t, ac3Stereo48k, 6, 0x00), av.AC3, 48000, av.CH_STEREO, 0, 32 * time.Millisecond},
		// bsid 9 and 10: half and quarter sample rate
		{"ac-3 bsid 9", modified(t, ac3Stereo48k, 5, 0x48), av.AC3, 24000, av.CH_STEREO, 2, 64 * time.Millisecond},
		{"ac-3 bsid 10", modified(t, ac3Stereo48k, 5, 0x50), av.AC3, 12000, av.CH_STEREO, 2, 128 * time.Millisecond},
		{"e-ac-3 stereo", decodeHex(t, eac3Stereo48k), av.EAC3, 48000, av.CH_STEREO, 2, 32 * time.Millisecond},
		{"e-ac-3 5.1", decodeHex(t, eac3Surround), av.EAC3, 48000, surround51, 7, 32 * time.Millisecond},
		{"e-ac-3 32 kHz", decodeHex(t, eac3Stereo32k), av.EAC3, 32000, av.CH_STEREO, 2, 48 * time.Millisecond},
		// 1, 2 and 3 blocks
		{"e-ac-3 1 block", modified(t, eac3Stereo48k, 4, 0x04), av.EAC3, 48000, av.CH_STEREO, 2, 256 * time.Second / 48000},
		{"e-ac-3 2 blocks", modified(t, eac3Stereo48k, 4, 0x14), av.EAC3, 48000, av.CH_STEREO, 2, 512 * time.Second / 48000},
		{"e-ac-3 3 blocks", modified(t, eac3Stereo48k, 4, 0x24), av.EAC3, 48000, av.CH_STEREO, 2, 768 * time.Second / 48000},
		// the reduced sample rates (fscod 3), always 6 blocks
		{"e-ac-3 24 kHz", modified(t, eac3Stereo48k, 4, 0xc4), av.EAC3, 24000, av.CH_STEREO, 2, 64 * time.Millisecond},
		{"e-ac-3 22.05 kHz", modified(t, eac3Stereo48k, 4, 0xd4), av.EAC3, 22050, av.CH_STEREO, 2, 1536 * time.Second / 22050},
		{"e-ac-3 16 kHz", modified(t, eac3Stereo48k, 4, 0xe4), av.EAC3, 16000, av.CH_STEREO, 2, 96 * time.Millisecond},
		// converted from AC-3
		{"e-ac-3 stream type 2", modified(t, eac3Stereo48k, 2, 0x81), av.EAC3, 48000, av.CH_STEREO, 2, 32 * time.Millisecond},
		// mono with LFE
		{"e-ac-3 1.1", modified(t, eac3Stereo48k, 4, 0x33), av.EAC3, 48000, av.CH_MONO | av.CH_LOW_FREQ, 1, 32 * time.Millisecond},
	} {
		codec, err := NewCodecDataFromFrame(tc.frame)
		require.NoError(t, err, tc.name)
		require.Equal(t, tc.typ, codec.Type(), tc.name)
		require.Equal(t, tc.sampleRate, codec.SampleRate(), tc.name)
		require.Equal(t, tc.layout, codec.ChannelLayout(), tc.name)
		require.Equal(t, tc.mode, codec.ChannelMode, tc.name)
		require.Equal(t, av.FLTP, codec.SampleFormat(), tc.name)
		duration, err := codec.PacketDuration(tc.frame)
		require.NoError(t, err, tc.name)
		require.Equal(t, tc.duration, duration, tc.name)
	}
}

func TestChannelLayouts(t *testing.T) {
	// acmod: 1+1, 1/0, 2/0, 3/0, 2/1, 3/1, 2/2, 3/2
	channels := []int{2, 1, 2, 3, 3, 4, 4, 5}
	for mode, n := range channels {
		h := FrameHeader{ChannelMode: mode}
		require.Equal(t, n, h.ChannelLayout().Count(), "acmod %d", mode)
		h.LFE = true
		require.Equal(t, n+1, h.ChannelLayout().Count(), "acmod %d", mode)
	}
}

func TestNewCodecDataFromFrameInvalid(t *testing.T) {
	for _, tc := range []struct {
		name  string
		frame []byte
	}{
		{"empty", nil},
		{"too short", decodeHex(t, ac3Stereo48k)[:7]},
		{"no syncword", modified(t, ac3Stereo48k, 1, 0x78)},
		{"no syncword 2", modified(t, ac3Stereo48k, 0, 0x0c)},
		{"ac-3 reserved sample rate", modified(t, ac3Stereo48k, 4, 0xd4)},
		{"e-ac-3 reserved sample rate", modified(t, eac3Stereo48k, 4, 0xf4)},
		{"e-ac-3 reserved stream type", modified(t, eac3Stereo48k, 2, 0xc1)},
		{"e-ac-3 dependent substream first", modified(t, eac3Stereo48k, 2, 0x41)},
		{"bsid 17", modified(t, ac3Stereo48k, 5, 0x88)},
	} {
		_, err := NewCodecDataFromFrame(tc.frame)
		require.Error(t, err, tc.name)
	}

	// a dependent substream has a header, but can't start a stream
	h, err := ParseFrameHeader(modified(t, eac3Stereo48k, 2, 0x41))
	require.NoError(t, err)
	require.Equal(t, 1, h.StreamType)

	codec, err := NewCodecDataFromFrame(decodeHex(t, ac3Stereo48k))
	require.NoError(t, err)
	_, err = codec.PacketDuration([]byte{0x0b, 0x77})
	require.Error(t, err)
}
