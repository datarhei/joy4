package screenvideoparser

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
		name        string
		typ         av.CodecType
		frame       string
		width       int
		height      int
		blockWidth  int
		blockHeight int
	}{
		// the start of frames from FFmpeg's encoders (flashsv, flashsv2), with 64x64 blocks
		{"v1 320x240", av.FLASHSV, "314030f001", 320, 240, 64, 64},
		{"v1 200x150", av.FLASHSV, "30c8309601", 200, 150, 64, 64},
		{"v1 1280x720", av.FLASHSV, "350032d000", 1280, 720, 64, 64},
		{"v2 320x240", av.FLASHSV2, "314030f000", 320, 240, 64, 64},
		{"v2 640x480", av.FLASHSV2, "328031e000", 640, 480, 64, 64},
		// the smallest and the largest block size, the largest image size
		{"16x256 blocks", av.FLASHSV, "0fffffff", 4095, 4095, 16, 256},
	} {
		data := decodeHex(t, tc.frame)
		h, err := ParseFrameHeader(data)
		require.NoError(t, err, tc.name)
		require.Equal(t, FrameHeader{BlockWidth: tc.blockWidth, BlockHeight: tc.blockHeight, Width: tc.width, Height: tc.height}, h, tc.name)

		codec, err := NewCodecDataFromFrame(tc.typ, data)
		require.NoError(t, err, tc.name)
		require.Equal(t, tc.typ, codec.Type(), tc.name)
		require.Equal(t, tc.width, codec.Width(), tc.name)
		require.Equal(t, tc.height, codec.Height(), tc.name)
	}
}

func TestNewCodecDataFromFrameInvalid(t *testing.T) {
	for _, tc := range []struct {
		name  string
		typ   av.CodecType
		frame []byte
	}{
		{"empty", av.FLASHSV, nil},
		{"too short", av.FLASHSV, decodeHex(t, "314030")},
		{"no width", av.FLASHSV, decodeHex(t, "300030f0")},
		{"no height", av.FLASHSV2, decodeHex(t, "31403000")},
		{"not Screen video", av.H264, decodeHex(t, "314030f0")},
	} {
		_, err := NewCodecDataFromFrame(tc.typ, tc.frame)
		require.Error(t, err, tc.name)
	}
}
