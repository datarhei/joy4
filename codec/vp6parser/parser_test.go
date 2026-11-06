package vp6parser

import (
	"encoding/hex"
	"testing"

	"github.com/datarhei/joy4/av"
	"github.com/stretchr/testify/require"
)

func decodeHex(t *testing.T, s string) []byte {
	b, err := hex.DecodeString(s)
	require.NoError(t, err)
	return b
}

func TestNewCodecDataFromFrame(t *testing.T) {
	for _, tc := range []struct {
		name       string
		typ        av.CodecType
		adjustment uint8
		frame      string
		width      int
		height     int
	}{
		// the size adjustment and the start of the first key frame of sample files of FFmpeg
		{"512x384", av.VP6, 0x00, "784618201820", 512, 384},
		{"112x80", av.VP6, 0x00, "784605070507", 112, 80},
		{"480x272", av.VP6, 0x00, "7846111e111e", 480, 272},
		{"alpha 300x180", av.VP6A, 0x4c, "0010f46a460c130c13", 300, 180},
		{"alpha 310x449", av.VP6A, 0xaf, "00002f78461d141d14", 310, 449},
		// with the coefficient offset: separated coefficients, or no filter header
		{"separated coefficients", av.VP6, 0x00, "7946001018201820", 512, 384},
		{"no filter header", av.VP6, 0x00, "7840001018201820", 512, 384},
	} {
		codec, err := NewCodecDataFromFrame(tc.typ, tc.adjustment, decodeHex(t, tc.frame))
		require.NoError(t, err, tc.name)
		require.Equal(t, tc.typ, codec.Type(), tc.name)
		require.Equal(t, tc.adjustment, codec.Adjustment, tc.name)
		require.Equal(t, tc.width, codec.Width(), tc.name)
		require.Equal(t, tc.height, codec.Height(), tc.name)
	}
}

func TestParseFrameHeader(t *testing.T) {
	h, err := ParseFrameHeader(decodeHex(t, "784618201820"))
	require.NoError(t, err)
	require.Equal(t, FrameHeader{KeyFrame: true, Version: 8, Rows: 24, Cols: 32}, h)

	// an inter frame has no size
	h, err = ParseFrameHeader(decodeHex(t, "ee41315fe2ff"))
	require.NoError(t, err)
	require.Equal(t, FrameHeader{}, h)
}

func TestNewCodecDataFromFrameInvalid(t *testing.T) {
	for _, tc := range []struct {
		name  string
		typ   av.CodecType
		frame string
	}{
		{"empty", av.VP6, ""},
		{"key frame too short", av.VP6, "78"},
		{"no size", av.VP6, "7846"},
		{"no size after the coefficient offset", av.VP6, "79460010"},
		{"version 9", av.VP6, "784e18201820"},
		{"no rows", av.VP6, "784600201820"},
		{"no columns", av.VP6, "784618001820"},
		{"inter frame", av.VP6, "ee41315fe2ff"},
		{"no alpha offset", av.VP6A, "0010"},
		{"alpha inter frame", av.VP6A, "0001f2e6589c"},
		{"not VP6", av.H264, "784618201820"},
	} {
		_, err := NewCodecDataFromFrame(tc.typ, 0, decodeHex(t, tc.frame))
		require.Error(t, err, tc.name)
	}
}
