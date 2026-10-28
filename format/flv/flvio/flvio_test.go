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

func TestVideoCompositionTime(t *testing.T) {
	b := make([]byte, MaxTagSubHeaderLength)

	for _, tc := range []struct {
		cts, expected int64
	}{
		{0, 0},
		{40, 40},
		{-33, -33},
		{-40, -40},
		{0x7FFFFF, 0x7FFFFF},
		{-0x800000, -0x800000},
		{0x7FFFFF + 1, 0x7FFFFF},   // limited to SI24
		{-0x800000 - 1, -0x800000}, // limited to SI24
	} {
		for _, tag := range []Tag{
			{Type: TAG_VIDEO, FrameType: FRAME_INTER, CodecID: VIDEO_H264, AVCPacketType: AVC_NALU},
			{Type: TAG_VIDEO, IsExHeader: true, FrameType: FRAME_INTER, PacketType: PKTTYPE_CODED_FRAMES, FourCC: FOURCC_HEVC},
			{Type: TAG_VIDEO, IsExHeader: true, FrameType: FRAME_INTER, PacketType: PKTTYPE_CODED_FRAMES, FourCC: FOURCC_VVC},
		} {
			tag.CompositionTime = tc.cts
			n := tag.FillHeader(b)

			parsed := Tag{Type: TAG_VIDEO}
			m, err := parsed.ParseHeader(b[:n])
			require.NoError(t, err)
			require.Equal(t, n, m)
			require.Equal(t, tc.expected, parsed.CompositionTime, "cts %d, codec %d, fourcc %s", tc.cts, tag.CodecID, tag.FourCC[:])
		}
	}
}

func TestVideoParseHeaderLegacyCodecs(t *testing.T) {
	// Sorenson H.263 (codec id 2), keyframe: only the first byte is the header
	tag := Tag{Type: TAG_VIDEO}
	n, err := tag.ParseHeader([]byte{0x12, 0x00, 0x00, 0x84, 0x00})
	require.NoError(t, err)
	require.Equal(t, 1, n)
	require.Equal(t, uint8(2), tag.CodecID)
	require.Equal(t, uint8(FRAME_KEY), tag.FrameType)
	require.Equal(t, uint8(PKTTYPE_CODED_FRAMES), tag.PacketType)
	require.Equal(t, int64(0), tag.CompositionTime)

	// shorter than an AVC header
	tag = Tag{Type: TAG_VIDEO}
	n, err = tag.ParseHeader([]byte{0x22, 0x00})
	require.NoError(t, err)
	require.Equal(t, 1, n)

	// AVC (codec id 7), NALU with composition time 40
	tag = Tag{Type: TAG_VIDEO}
	n, err = tag.ParseHeader([]byte{0x17, 0x01, 0x00, 0x00, 0x28, 0xAA})
	require.NoError(t, err)
	require.Equal(t, 5, n)
	require.Equal(t, uint8(PKTTYPE_CODED_FRAMES), tag.PacketType)
	require.Equal(t, int64(40), tag.CompositionTime)

	// AVC without enough bytes for the AVC header
	tag = Tag{Type: TAG_VIDEO}
	_, err = tag.ParseHeader([]byte{0x17, 0x01, 0x00})
	require.Error(t, err)
}

func TestVideoFillHeaderLegacyCodecs(t *testing.T) {
	b := make([]byte, MaxTagSubHeaderLength)

	tag := Tag{Type: TAG_VIDEO, FrameType: FRAME_KEY, CodecID: 2}
	n := tag.FillHeader(b)
	require.Equal(t, []byte{0x12}, b[:n])

	tag = Tag{Type: TAG_VIDEO, FrameType: FRAME_KEY, CodecID: VIDEO_H264, AVCPacketType: AVC_NALU, CompositionTime: 40}
	n = tag.FillHeader(b)
	require.Equal(t, []byte{0x17, 0x01, 0x00, 0x00, 0x28}, b[:n])
}
