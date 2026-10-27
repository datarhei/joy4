package flvio

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestVideoParseHeaderShortHEVCCodedFrames(t *testing.T) {
	// ExHeader, keyframe, PacketTypeCodedFrames, FourCC "hvc1", but no composition time
	for _, b := range [][]byte{
		{0x91, 'h', 'v', 'c', '1'},
		{0x91, 'h', 'v', 'c', '1', 0x00},
		{0x91, 'h', 'v', 'c', '1', 0x00, 0x00},
	} {
		tag := Tag{Type: TAG_VIDEO}
		_, err := tag.ParseHeader(b)
		require.Error(t, err, "%d bytes", len(b))
	}
}

func TestVideoParseHeaderHEVCCodedFrames(t *testing.T) {
	b := []byte{0x91, 'h', 'v', 'c', '1', 0xFF, 0xFF, 0xDF, 0xAA}

	tag := Tag{Type: TAG_VIDEO}
	n, err := tag.ParseHeader(b)
	require.NoError(t, err)
	require.Equal(t, 8, n)
	require.Equal(t, int64(-33), tag.CompositionTime)
}
