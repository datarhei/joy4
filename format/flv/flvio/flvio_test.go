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

// ffmpegColorInfo is a video message with the color info as FFmpeg 9 writes it for HEVC (bt2020, smpte2084,
// bt2020nc): ExHeader, PacketTypeMetadata with the frame type bits of a command frame, FourCC "hvc1", and
// ["colorInfo", {colorConfig: {transferCharacteristics: 16, matrixCoefficients: 9, colorPrimaries: 9}}].
var ffmpegColorInfo = []byte{
	0xd4, 0x68, 0x76, 0x63, 0x31, 0x02, 0x00, 0x09, 0x63, 0x6f, 0x6c, 0x6f, 0x72, 0x49, 0x6e, 0x66,
	0x6f, 0x03, 0x00, 0x0b, 0x63, 0x6f, 0x6c, 0x6f, 0x72, 0x43, 0x6f, 0x6e, 0x66, 0x69, 0x67, 0x03,
	0x00, 0x17, 0x74, 0x72, 0x61, 0x6e, 0x73, 0x66, 0x65, 0x72, 0x43, 0x68, 0x61, 0x72, 0x61, 0x63,
	0x74, 0x65, 0x72, 0x69, 0x73, 0x74, 0x69, 0x63, 0x73, 0x00, 0x40, 0x30, 0x00, 0x00, 0x00, 0x00,
	0x00, 0x00, 0x00, 0x12, 0x6d, 0x61, 0x74, 0x72, 0x69, 0x78, 0x43, 0x6f, 0x65, 0x66, 0x66, 0x69,
	0x63, 0x69, 0x65, 0x6e, 0x74, 0x73, 0x00, 0x40, 0x22, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00,
	0x0e, 0x63, 0x6f, 0x6c, 0x6f, 0x72, 0x50, 0x72, 0x69, 0x6d, 0x61, 0x72, 0x69, 0x65, 0x73, 0x00,
	0x40, 0x22, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x09, 0x00, 0x00, 0x09,
}

var ffmpegColorInfoValue = AMFMap{
	"colorConfig": AMFMap{
		"transferCharacteristics": 16.0,
		"matrixCoefficients":      9.0,
		"colorPrimaries":          9.0,
	},
}

func TestVideoParseHeaderMetadata(t *testing.T) {
	tag := Tag{Type: TAG_VIDEO}
	n, err := tag.ParseHeader(ffmpegColorInfo)
	require.NoError(t, err)
	require.Equal(t, 5, n)
	require.True(t, tag.IsExHeader)
	require.Equal(t, uint8(PKTTYPE_METADATA), tag.PacketType)
	require.Equal(t, FOURCC_HEVC, tag.FourCC)

	colorInfo, ok := ParseColorInfo(ffmpegColorInfo[n:])
	require.True(t, ok)
	require.Equal(t, ffmpegColorInfoValue, colorInfo)

	// writing it again gives the same header
	b := make([]byte, MaxTagSubHeaderLength)
	require.Equal(t, ffmpegColorInfo[:n], b[:tag.FillHeader(b)])
}

func amf0Data(vals ...interface{}) []byte {
	b := []byte{}
	for _, val := range vals {
		v := make([]byte, LenAMF0Val(val))
		FillAMF0Val(v, val)
		b = append(b, v...)
	}
	return b
}

func TestParseColorInfo(t *testing.T) {
	info := AMFMap{"hdrCll": AMFMap{"maxFall": 400.0, "maxCLL": 1000.0}}

	for _, tc := range []struct {
		name      string
		data      []byte
		colorInfo AMFMap
		ok        bool
	}{
		{"object", amf0Data("colorInfo", info), info, true},
		{"undefined", append(amf0Data("colorInfo"), undefinedmarker), nil, true},
		{"null", amf0Data("colorInfo", nil), nil, true},
		{"empty object", amf0Data("colorInfo", AMFMap{}), nil, true},
		{"other pairs", amf0Data("other", 1.0, "colorInfo", info, 2.0, "x"), info, true},
		{"last pair", amf0Data("colorInfo", AMFMap{"a": 1.0}, "colorInfo", info), info, true},
		{"no color info", amf0Data("other", info), nil, false},
		{"other type", amf0Data("colorInfo", "x"), nil, false},
		{"empty", []byte{}, nil, false},
		{"invalid", []byte{0x02, 0x00, 0x09, 'c'}, nil, false},
		{"missing value", amf0Data("colorInfo"), nil, false},
		{"invalid after color info", append(amf0Data("colorInfo", info), 0xFF), info, true},
	} {
		colorInfo, ok := ParseColorInfo(tc.data)
		require.Equal(t, tc.ok, ok, tc.name)
		require.Equal(t, tc.colorInfo, colorInfo, tc.name)
	}
}

func TestColorInfoData(t *testing.T) {
	colorInfo, ok := ParseColorInfo(ColorInfoData(ffmpegColorInfoValue))
	require.True(t, ok)
	require.Equal(t, ffmpegColorInfoValue, colorInfo)

	// a reset is written as undefined
	data := ColorInfoData(nil)
	require.Equal(t, append(amf0Data("colorInfo"), undefinedmarker), data)
	colorInfo, ok = ParseColorInfo(data)
	require.True(t, ok)
	require.Nil(t, colorInfo)
}

func TestVideoParseHeaderCommandFrame(t *testing.T) {
	// ExHeader command frame (E-RTMP v2): the command follows the first byte, there's no FourCC
	tag := Tag{Type: TAG_VIDEO}
	n, err := tag.ParseHeader([]byte{0xD0, 0x01})
	require.NoError(t, err)
	require.Equal(t, 1, n)
	require.True(t, tag.IsExHeader)
	require.Equal(t, uint8(FRAME_COMMAND), tag.FrameType)

	b := make([]byte, MaxTagSubHeaderLength)
	require.Equal(t, []byte{0xD0}, b[:tag.FillHeader(b)])

	// without the command
	tag = Tag{Type: TAG_VIDEO}
	_, err = tag.ParseHeader([]byte{0xD0})
	require.Error(t, err)

	// legacy AVC command frame
	tag = Tag{Type: TAG_VIDEO}
	n, err = tag.ParseHeader([]byte{0x57, 0x01})
	require.NoError(t, err)
	require.Equal(t, 1, n)
	require.Equal(t, uint8(FRAME_COMMAND), tag.FrameType)
}
