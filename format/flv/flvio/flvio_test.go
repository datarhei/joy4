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

func TestVideoParseHeaderVVC(t *testing.T) {
	// CodedFrames with composition time 40
	tag := Tag{Type: TAG_VIDEO}
	n, err := tag.ParseHeader([]byte{0x91, 'v', 'v', 'c', '1', 0x00, 0x00, 0x28, 0xAA})
	require.NoError(t, err)
	require.Equal(t, 8, n)
	require.Equal(t, FOURCC_VVC, tag.FourCC)
	require.Equal(t, uint8(PKTTYPE_CODED_FRAMES), tag.PacketType)
	require.Equal(t, int64(40), tag.CompositionTime)

	// CodedFramesX without composition time
	tag = Tag{Type: TAG_VIDEO}
	n, err = tag.ParseHeader([]byte{0x93, 'v', 'v', 'c', '1', 0xAA})
	require.NoError(t, err)
	require.Equal(t, 5, n)
	require.Equal(t, uint8(PKTTYPE_CODED_FRAMESX), tag.PacketType)
	require.Equal(t, int64(0), tag.CompositionTime)

	// CodedFrames without enough bytes for the composition time
	tag = Tag{Type: TAG_VIDEO}
	_, err = tag.ParseHeader([]byte{0x91, 'v', 'v', 'c', '1', 0x00})
	require.Error(t, err)
}

func TestVideoFillHeaderVVC(t *testing.T) {
	b := make([]byte, MaxTagSubHeaderLength)

	tag := Tag{
		Type:            TAG_VIDEO,
		IsExHeader:      true,
		FrameType:       FRAME_KEY,
		PacketType:      PKTTYPE_CODED_FRAMES,
		FourCC:          FOURCC_VVC,
		CompositionTime: 40,
	}
	n := tag.FillHeader(b)
	require.Equal(t, []byte{0x91, 'v', 'v', 'c', '1', 0x00, 0x00, 0x28}, b[:n])

	tag.PacketType = PKTTYPE_CODED_FRAMESX
	tag.CompositionTime = 0
	n = tag.FillHeader(b)
	require.Equal(t, []byte{0x93, 'v', 'v', 'c', '1'}, b[:n])
}
