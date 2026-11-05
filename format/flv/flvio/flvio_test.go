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

func TestVideoParseHeaderModEx(t *testing.T) {
	// HEVC CodedFrames with a TimestampOffsetNano of 500000 ns: ExHeader keyframe with PacketTypeModEx,
	// size 3 (UI8 + 1), the offset (UI24), ModEx type 0 and the packet type CodedFrames, the FourCC and
	// the composition time
	b := []byte{0x97, 0x02, 0x07, 0xA1, 0x20, 0x01, 'h', 'v', 'c', '1', 0x00, 0x00, 0x28, 0xAA}
	tag := Tag{Type: TAG_VIDEO}
	n, err := tag.ParseHeader(b)
	require.NoError(t, err)
	require.Equal(t, len(b)-1, n)
	require.True(t, tag.IsExHeader)
	require.Equal(t, uint8(FRAME_KEY), tag.FrameType)
	require.Equal(t, uint8(PKTTYPE_CODED_FRAMES), tag.PacketType)
	require.Equal(t, FOURCC_HEVC, tag.FourCC)
	require.Equal(t, int64(40), tag.CompositionTime)
	require.Equal(t, uint32(500000), tag.TimestampNanoOffset)

	// every truncation is an error
	for i := 1; i < n; i++ {
		tag := Tag{Type: TAG_VIDEO}
		_, err := tag.ParseHeader(b[:i])
		require.Error(t, err, "%d bytes", i)
	}

	// it isn't written
	w := make([]byte, MaxTagSubHeaderLength)
	require.Equal(t, []byte{0x91, 'h', 'v', 'c', '1', 0x00, 0x00, 0x28}, w[:tag.FillHeader(w)])
}

func TestVideoParseHeaderModExSizes(t *testing.T) {
	// a 16 bit size: UI8 255 (256), then UI16 299 (300 bytes), an unknown ModEx type 5, followed by a
	// TimestampOffsetNano of 1000 ns with a 4 byte data (only the first 3 bytes are used)
	b := []byte{0x97, 0xFF, 0x01, 0x2B}
	b = append(b, make([]byte, 300)...)
	b = append(b, 0x57, 0x03, 0x00, 0x03, 0xE8, 0xFF, 0x00, 'a', 'v', '0', '1', 0xAA)
	tag := Tag{Type: TAG_VIDEO}
	n, err := tag.ParseHeader(b)
	require.NoError(t, err)
	require.Equal(t, len(b)-1, n)
	require.Equal(t, uint8(PKTTYPE_SEQUENCE_START), tag.PacketType)
	require.Equal(t, FOURCC_AV1, tag.FourCC)
	require.Equal(t, uint32(1000), tag.TimestampNanoOffset)

	// a TimestampOffsetNano with less than 3 bytes is ignored
	tag = Tag{Type: TAG_VIDEO}
	n, err = tag.ParseHeader([]byte{0x97, 0x01, 0x03, 0xE8, 0x03, 'a', 'v', '0', '1', 0xAA})
	require.NoError(t, err)
	require.Equal(t, 9, n)
	require.Equal(t, uint8(PKTTYPE_CODED_FRAMESX), tag.PacketType)
	require.Equal(t, uint32(0), tag.TimestampNanoOffset)
}

func TestVideoParseHeaderModExMetadataCommand(t *testing.T) {
	// the frame type of metadata after ModEx is kept (it is to be ignored)
	tag := Tag{Type: TAG_VIDEO}
	n, err := tag.ParseHeader([]byte{0xD7, 0x02, 0x00, 0x00, 0x01, 0x04, 'v', 'v', 'c', '1', 0xAA})
	require.NoError(t, err)
	require.Equal(t, 10, n)
	require.Equal(t, uint8(PKTTYPE_METADATA), tag.PacketType)
	require.Equal(t, uint8(0b1101), tag.FrameType)
	require.Equal(t, FOURCC_VVC, tag.FourCC)

	// a command frame after ModEx has the command instead of a FourCC
	tag = Tag{Type: TAG_VIDEO}
	n, err = tag.ParseHeader([]byte{0xD7, 0x02, 0x00, 0x00, 0x01, 0x01, 0x01})
	require.NoError(t, err)
	require.Equal(t, 6, n)
	require.Equal(t, uint8(FRAME_COMMAND), tag.FrameType)
	require.Equal(t, uint8(PKTTYPE_CODED_FRAMES), tag.PacketType)
}

func TestAudioParseHeaderExHeader(t *testing.T) {
	for _, tc := range []struct {
		name       string
		data       []byte
		packetType uint8
		fourCC     [4]byte
	}{
		{"sequence start", []byte{0x90, 'm', 'p', '4', 'a', 0x12, 0x10}, PKTTYPE_SEQUENCE_START, FOURCC_AAC},
		{"coded frames", []byte{0x91, 'm', 'p', '4', 'a', 0xAA}, PKTTYPE_CODED_FRAMES, FOURCC_AAC},
		{"sequence end", []byte{0x92, 'O', 'p', 'u', 's', 0xAA}, PKTTYPE_SEQUENCE_END, FOURCC_OPUS},
		// as FFmpeg writes it for mono Opus
		{"multichannel config", []byte{0x94, 'O', 'p', 'u', 's', 0x01, 0x01, 0x00, 0x00, 0x00, 0x04}, AUDIO_PKTTYPE_MULTICHANNEL_CONFIG, FOURCC_OPUS},
	} {
		tag := Tag{Type: TAG_AUDIO}
		n, err := tag.ParseHeader(tc.data)
		require.NoError(t, err, tc.name)
		require.Equal(t, 5, n, tc.name)
		require.True(t, tag.IsExHeader, tc.name)
		require.Equal(t, uint8(SOUND_EXHEADER), tag.SoundFormat, tc.name)
		require.Equal(t, tc.packetType, tag.PacketType, tc.name)
		require.Equal(t, tc.fourCC, tag.FourCC, tc.name)

		b := make([]byte, MaxTagSubHeaderLength)
		require.Equal(t, tc.data[:5], b[:tag.FillHeader(b)], tc.name)

		for i := 1; i < 5; i++ {
			tag := Tag{Type: TAG_AUDIO}
			_, err := tag.ParseHeader(tc.data[:i])
			require.Error(t, err, "%s: %d bytes", tc.name, i)
		}
	}
}

func TestAudioParseHeaderExHeaderModEx(t *testing.T) {
	// CodedFrames with a TimestampOffsetNano of 500000 ns
	b := []byte{0x97, 0x02, 0x07, 0xA1, 0x20, 0x01, 'm', 'p', '4', 'a', 0xAA}
	tag := Tag{Type: TAG_AUDIO}
	n, err := tag.ParseHeader(b)
	require.NoError(t, err)
	require.Equal(t, len(b)-1, n)
	require.Equal(t, uint8(PKTTYPE_CODED_FRAMES), tag.PacketType)
	require.Equal(t, FOURCC_AAC, tag.FourCC)
	require.Equal(t, uint32(500000), tag.TimestampNanoOffset)

	for i := 1; i < n; i++ {
		tag := Tag{Type: TAG_AUDIO}
		_, err := tag.ParseHeader(b[:i])
		require.Error(t, err, "%d bytes", i)
	}
}

func TestAudioParseHeaderLegacy(t *testing.T) {
	tag := Tag{Type: TAG_AUDIO}
	n, err := tag.ParseHeader([]byte{0xAF, 0x01, 0xAA})
	require.NoError(t, err)
	require.Equal(t, 2, n)
	require.False(t, tag.IsExHeader)
	require.Equal(t, uint8(SOUND_AAC), tag.SoundFormat)
	require.Equal(t, uint8(AAC_RAW), tag.AACPacketType)

	b := make([]byte, MaxTagSubHeaderLength)
	require.Equal(t, []byte{0xAF, 0x01}, b[:tag.FillHeader(b)])
}

func TestParseMultichannelConfig(t *testing.T) {
	// 5.1 as FFmpeg writes it for Opus
	config, err := ParseMultichannelConfig([]byte{0x01, 0x06, 0x00, 0x00, 0x00, 0x3f})
	require.NoError(t, err)
	require.Equal(t, MultichannelConfig{ChannelOrder: AUDIO_CHANNEL_ORDER_NATIVE, ChannelCount: 6, ChannelFlags: 0x3f}, config)

	config, err = ParseMultichannelConfig([]byte{0x02, 0x02, 0x01, 0x00})
	require.NoError(t, err)
	require.Equal(t, MultichannelConfig{ChannelOrder: AUDIO_CHANNEL_ORDER_CUSTOM, ChannelCount: 2, ChannelMapping: []uint8{0x01, 0x00}}, config)

	config, err = ParseMultichannelConfig([]byte{0x00, 0x08})
	require.NoError(t, err)
	require.Equal(t, MultichannelConfig{ChannelOrder: AUDIO_CHANNEL_ORDER_UNSPECIFIED, ChannelCount: 8}, config)

	for _, b := range [][]byte{{}, {0x00}, {0x01, 0x06, 0x00, 0x00, 0x00}, {0x02, 0x02, 0x01}} {
		_, err := ParseMultichannelConfig(b)
		require.Error(t, err, "%x", b)
	}
}

func TestMultichannelConfigBytes(t *testing.T) {
	for _, b := range [][]byte{{0x01, 0x06, 0x00, 0x00, 0x00, 0x3f}, {0x02, 0x02, 0x01, 0x00}, {0x00, 0x08}} {
		config, err := ParseMultichannelConfig(b)
		require.NoError(t, err)
		require.Equal(t, b, config.Bytes())
	}
}

func TestParseHeaderMultitrackOneTrack(t *testing.T) {
	for _, tc := range []struct {
		name       string
		tagType    uint8
		data       []byte
		n          int
		packetType uint8
		fourCC     [4]byte
		trackID    uint8
		cts        int64
	}{
		// as FFmpeg writes the second video and audio track
		{"video sequence start", TAG_VIDEO, []byte{0x96, 0x00, 'a', 'v', 'c', '1', 0x01, 0x01, 0xf4}, 7, PKTTYPE_SEQUENCE_START, FOURCC_AVC, 1, 0},
		{"video coded frames x", TAG_VIDEO, []byte{0x96, 0x03, 'a', 'v', 'c', '1', 0x01, 0x00, 0x00}, 7, PKTTYPE_CODED_FRAMESX, FOURCC_AVC, 1, 0},
		{"video coded frames", TAG_VIDEO, []byte{0x96, 0x01, 'h', 'v', 'c', '1', 0x02, 0x00, 0x00, 0x28, 0xAA}, 10, PKTTYPE_CODED_FRAMES, FOURCC_HEVC, 2, 40},
		{"audio sequence start", TAG_AUDIO, []byte{0x95, 0x00, 'm', 'p', '4', 'a', 0x01, 0x11, 0x88}, 7, PKTTYPE_SEQUENCE_START, FOURCC_AAC, 1, 0},
		{"audio multichannel config", TAG_AUDIO, []byte{0x95, 0x04, 'm', 'p', '4', 'a', 0x01, 0x01, 0x01, 0x00, 0x00, 0x00, 0x04}, 7, AUDIO_PKTTYPE_MULTICHANNEL_CONFIG, FOURCC_AAC, 1, 0},
	} {
		tag := Tag{Type: tc.tagType}
		n, err := tag.ParseHeader(tc.data)
		require.NoError(t, err, tc.name)
		require.Equal(t, tc.n, n, tc.name)
		require.True(t, tag.IsExHeader, tc.name)
		require.True(t, tag.IsMultitrack, tc.name)
		require.Equal(t, uint8(MULTITRACK_ONE_TRACK), tag.MultitrackType, tc.name)
		require.Equal(t, tc.packetType, tag.PacketType, tc.name)
		require.Equal(t, tc.fourCC, tag.FourCC, tc.name)
		require.Equal(t, tc.trackID, tag.TrackID, tc.name)
		require.Equal(t, tc.cts, tag.CompositionTime, tc.name)

		tag.Data = tc.data[n:]
		tags, err := SplitTracks(tag)
		require.NoError(t, err, tc.name)
		require.Equal(t, []Tag{tag}, tags, tc.name)

		b := make([]byte, MaxTagSubHeaderLength)
		require.Equal(t, tc.data[:n], b[:tag.FillHeader(b)], tc.name)

		for i := 1; i < n; i++ {
			tag := Tag{Type: tc.tagType}
			_, err := tag.ParseHeader(tc.data[:i])
			require.Error(t, err, "%s: %d bytes", tc.name, i)
		}
	}
}

func TestParseHeaderMultitrackManyTracks(t *testing.T) {
	// HEVC coded frames of the tracks 0 and 1, each with its composition time
	data := []byte{0x96, 0x11, 'h', 'v', 'c', '1',
		0x00, 0x00, 0x00, 0x05, 0x00, 0x00, 0x28, 0xA0, 0xA1,
		0x01, 0x00, 0x00, 0x04, 0x00, 0x00, 0x50, 0xB0,
	}
	tag := Tag{Type: TAG_VIDEO}
	n, err := tag.ParseHeader(data)
	require.NoError(t, err)
	require.Equal(t, 6, n)
	require.True(t, tag.IsMultitrack)
	require.Equal(t, uint8(MULTITRACK_MANY_TRACKS), tag.MultitrackType)
	require.Equal(t, uint8(FRAME_KEY), tag.FrameType)
	require.Equal(t, uint8(PKTTYPE_CODED_FRAMES), tag.PacketType)
	require.Equal(t, FOURCC_HEVC, tag.FourCC)

	tag.Data = data[n:]
	tags, err := SplitTracks(tag)
	require.NoError(t, err)
	require.Len(t, tags, 2)
	for i, expected := range []struct {
		trackID uint8
		cts     int64
		data    []byte
	}{{0, 40, []byte{0xA0, 0xA1}}, {1, 80, []byte{0xB0}}} {
		require.Equal(t, expected.trackID, tags[i].TrackID, "track %d", i)
		require.Equal(t, expected.cts, tags[i].CompositionTime, "track %d", i)
		require.Equal(t, expected.data, tags[i].Data, "track %d", i)
		require.Equal(t, FOURCC_HEVC, tags[i].FourCC, "track %d", i)
		require.Equal(t, uint8(FRAME_KEY), tags[i].FrameType, "track %d", i)
	}

	// a track is written as OneTrack
	b := make([]byte, MaxTagSubHeaderLength)
	require.Equal(t, []byte{0x96, 0x01, 'h', 'v', 'c', '1', 0x01, 0x00, 0x00, 0x50}, b[:tags[1].FillHeader(b)])

	// every truncation of the tracks is an error, except at the end of a track
	for i := n; i < len(data); i++ {
		tag.Data = data[n:i]
		_, err := SplitTracks(tag)
		if i == n || i == n+9 {
			require.NoError(t, err, "%d bytes", i)
		} else {
			require.Error(t, err, "%d bytes", i)
		}
	}
}

func TestParseHeaderMultitrackManyTracksManyCodecs(t *testing.T) {
	// video: CodedFramesX of AVC (track 0) and HEVC (track 1)
	data := []byte{0x96, 0x23,
		'a', 'v', 'c', '1', 0x00, 0x00, 0x00, 0x01, 0xA0,
		'h', 'v', 'c', '1', 0x01, 0x00, 0x00, 0x02, 0xB0, 0xB1,
	}
	tag := Tag{Type: TAG_VIDEO}
	n, err := tag.ParseHeader(data)
	require.NoError(t, err)
	require.Equal(t, 2, n)
	require.Equal(t, uint8(MULTITRACK_MANY_TRACKS_MANY_CODECS), tag.MultitrackType)
	require.Equal(t, uint8(PKTTYPE_CODED_FRAMESX), tag.PacketType)

	tag.Data = data[n:]
	tags, err := SplitTracks(tag)
	require.NoError(t, err)
	require.Len(t, tags, 2)
	require.Equal(t, FOURCC_AVC, tags[0].FourCC)
	require.Equal(t, uint8(0), tags[0].TrackID)
	require.Equal(t, []byte{0xA0}, tags[0].Data)
	require.Equal(t, FOURCC_HEVC, tags[1].FourCC)
	require.Equal(t, uint8(1), tags[1].TrackID)
	require.Equal(t, []byte{0xB0, 0xB1}, tags[1].Data)

	// audio: AAC (track 0) and Opus (track 1)
	data = []byte{0x95, 0x21,
		'm', 'p', '4', 'a', 0x00, 0x00, 0x00, 0x01, 0xA0,
		'O', 'p', 'u', 's', 0x01, 0x00, 0x00, 0x01, 0xB0,
	}
	tag = Tag{Type: TAG_AUDIO}
	n, err = tag.ParseHeader(data)
	require.NoError(t, err)
	require.Equal(t, 2, n)
	tag.Data = data[n:]
	tags, err = SplitTracks(tag)
	require.NoError(t, err)
	require.Len(t, tags, 2)
	require.Equal(t, FOURCC_AAC, tags[0].FourCC)
	require.Equal(t, []byte{0xA0}, tags[0].Data)
	require.Equal(t, FOURCC_OPUS, tags[1].FourCC)
	require.Equal(t, uint8(1), tags[1].TrackID)
	require.Equal(t, []byte{0xB0}, tags[1].Data)

	b := make([]byte, MaxTagSubHeaderLength)
	require.Equal(t, []byte{0x95, 0x01, 'O', 'p', 'u', 's', 0x01}, b[:tags[1].FillHeader(b)])
}

func TestParseHeaderMultitrackInvalid(t *testing.T) {
	for _, tc := range []struct {
		name    string
		tagType uint8
		data    []byte
	}{
		{"nested", TAG_VIDEO, []byte{0x96, 0x06, 'a', 'v', 'c', '1', 0x01}},
		{"nested", TAG_AUDIO, []byte{0x95, 0x05, 'm', 'p', '4', 'a', 0x01}},
		{"unknown type", TAG_VIDEO, []byte{0x96, 0x31, 'a', 'v', 'c', '1', 0x01}},
		{"unknown type", TAG_AUDIO, []byte{0x95, 0x31, 'm', 'p', '4', 'a', 0x01}},
		{"no multitrack type", TAG_VIDEO, []byte{0x96}},
		{"no multitrack type", TAG_AUDIO, []byte{0x95}},
	} {
		tag := Tag{Type: tc.tagType}
		_, err := tag.ParseHeader(tc.data)
		require.Error(t, err, "%s %d", tc.name, tc.tagType)
	}
}
