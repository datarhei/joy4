package h263parser

import (
	"encoding/hex"
	"testing"

	"github.com/datarhei/joy4/av"
	"github.com/stretchr/testify/require"
)

// the start of frames from FFmpeg's FLV1 encoder: key frames and inter frames with standard sizes (352x288,
// 320x240) and custom sizes with 8 bits (200x150) and 16 bits (640x480)
const (
	key352   = "0000840112a62020"
	inter352 = "000084053131793e"
	key320   = "0000840292a62020"
	inter320 = "00008406b13c28b0"
	key200   = "00008400644b1226"
	key640   = "00008400814000f01326"
	inter640 = "00008404814000f0313f"
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

// withSize returns the 320x240 key frame with the size code, which are the last 2 bits of byte 3 and the
// first bit of byte 4.
func withSize(t *testing.T, code byte) []byte {
	data := decodeHex(t, key320)
	data[3] = data[3]&0xfc | code>>1
	data[4] = data[4]&0x7f | code&1<<7
	return data
}

func TestParsePictureHeader(t *testing.T) {
	for _, tc := range []struct {
		name        string
		frame       []byte
		width       int
		height      int
		pictureType int
	}{
		{"352x288 key", decodeHex(t, key352), 352, 288, PictureIntra},
		{"352x288 inter", decodeHex(t, inter352), 352, 288, PictureInter},
		{"320x240 key", decodeHex(t, key320), 320, 240, PictureIntra},
		{"320x240 inter", decodeHex(t, inter320), 320, 240, PictureInter},
		{"200x150 key", decodeHex(t, key200), 200, 150, PictureIntra},
		{"640x480 key", decodeHex(t, key640), 640, 480, PictureIntra},
		{"640x480 inter", decodeHex(t, inter640), 640, 480, PictureInter},
		// the other standard sizes: 176x144, 128x96, 160x120 (size code 3, 4, 6)
		{"176x144", withSize(t, 3), 176, 144, PictureIntra},
		{"128x96", withSize(t, 4), 128, 96, PictureIntra},
		{"320x240 again", withSize(t, 5), 320, 240, PictureIntra},
		{"160x120", withSize(t, 6), 160, 120, PictureIntra},
		// a disposable inter frame (picture type 2)
		{"disposable", modified(t, inter320, 4, 0xd1), 320, 240, PictureDisposable},
	} {
		h, err := ParsePictureHeader(tc.frame)
		require.NoError(t, err, tc.name)
		require.Equal(t, 1, h.Version, tc.name)
		require.Equal(t, tc.width, h.Width, tc.name)
		require.Equal(t, tc.height, h.Height, tc.name)
		require.Equal(t, tc.pictureType, h.PictureType, tc.name)
		require.Equal(t, tc.pictureType == PictureDisposable, IsDisposable(tc.frame), tc.name)

		codec, err := NewCodecDataFromFrame(tc.frame)
		require.NoError(t, err, tc.name)
		require.Equal(t, av.H263, codec.Type(), tc.name)
		require.Equal(t, tc.width, codec.Width(), tc.name)
		require.Equal(t, tc.height, codec.Height(), tc.name)
	}

	// version 0
	h, err := ParsePictureHeader(modified(t, key320, 2, 0x80))
	require.NoError(t, err)
	require.Equal(t, 0, h.Version)
}

func TestParsePictureHeaderInvalid(t *testing.T) {
	for _, tc := range []struct {
		name  string
		frame []byte
	}{
		{"empty", nil},
		{"no picture start code", modified(t, key320, 1, 0x01)},
		{"no picture start code 2", modified(t, key320, 2, 0x04)},
		{"version 2", modified(t, key320, 2, 0x88)},
		{"reserved size", withSize(t, 7)},
		{"reserved picture type", modified(t, inter320, 4, 0xf1)},
		{"no size", decodeHex(t, "0000840000000000")},
		{"too short", decodeHex(t, key320)[:4]},
		{"too short for the 8 bit size", decodeHex(t, key200)[:5]},
		{"too short for the 16 bit size", decodeHex(t, key640)[:8]},
	} {
		_, err := ParsePictureHeader(tc.frame)
		require.Error(t, err, tc.name)
		require.False(t, IsDisposable(tc.frame), tc.name)
	}
}
