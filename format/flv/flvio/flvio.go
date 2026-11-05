package flvio

import (
	"fmt"
	"io"

	"github.com/datarhei/joy4/av"
	"github.com/datarhei/joy4/utils/bits/pio"
)

func TsToTime(ts int64) int64 {
	return ts
}

func TimeToTs(tm int64) int64 {
	return tm
}

const MaxTagSubHeaderLength = 16

const (
	TAG_AUDIO      = 8
	TAG_VIDEO      = 9
	TAG_SCRIPTDATA = 18
)

const (
	SOUND_MP3                   = 2
	SOUND_NELLYMOSER_16KHZ_MONO = 4
	SOUND_NELLYMOSER_8KHZ_MONO  = 5
	SOUND_NELLYMOSER            = 6
	SOUND_ALAW                  = 7
	SOUND_MULAW                 = 8
	SOUND_EXHEADER              = 9 // ExAudioTagHeader (E-RTMP v2)
	SOUND_AAC                   = 10
	SOUND_SPEEX                 = 11
	SOUND_MP3_8KHZ              = 14

	SOUND_5_5Khz = 0
	SOUND_11Khz  = 1
	SOUND_22Khz  = 2
	SOUND_44Khz  = 3

	SOUND_8BIT  = 0
	SOUND_16BIT = 1

	SOUND_MONO   = 0
	SOUND_STEREO = 1

	AAC_SEQHDR = 0
	AAC_RAW    = 1
)

const (
	AVC_SEQHDR = 0
	AVC_NALU   = 1
	AVC_EOS    = 2

	FRAME_KEY     = 1
	FRAME_INTER   = 2
	FRAME_COMMAND = 5

	VIDEO_H264 = 7
)

const (
	PKTTYPE_SEQUENCE_START         = 0
	PKTTYPE_CODED_FRAMES           = 1
	PKTTYPE_SEQUENCE_END           = 2
	PKTTYPE_CODED_FRAMESX          = 3
	PKTTYPE_METADATA               = 4
	PKTTYPE_MPEG2TS_SEQUENCE_START = 5
	PKTTYPE_MULTITRACK             = 6
	PKTTYPE_MODEX                  = 7
)

// The audio packet types of the ExAudioTagHeader (E-RTMP v2) that differ from the video packet types.
// SequenceStart, CodedFrames, SequenceEnd and ModEx are the same as PKTTYPE_SEQUENCE_START,
// PKTTYPE_CODED_FRAMES, PKTTYPE_SEQUENCE_END and PKTTYPE_MODEX.
const (
	AUDIO_PKTTYPE_MULTICHANNEL_CONFIG = 4
	AUDIO_PKTTYPE_MULTITRACK          = 5
)

// The FourCCs of the audio codecs of the ExAudioTagHeader (E-RTMP v2).
var (
	FOURCC_AAC  = [4]byte{'m', 'p', '4', 'a'}
	FOURCC_OPUS = [4]byte{'O', 'p', 'u', 's'}
	FOURCC_FLAC = [4]byte{'f', 'L', 'a', 'C'}
	FOURCC_AC3  = [4]byte{'a', 'c', '-', '3'}
	FOURCC_EAC3 = [4]byte{'e', 'c', '-', '3'}
	FOURCC_MP3  = [4]byte{'.', 'm', 'p', '3'}
)

// The multitrack types (E-RTMP v2, AvMultitrackType).
const (
	MULTITRACK_ONE_TRACK               = 0
	MULTITRACK_MANY_TRACKS             = 1
	MULTITRACK_MANY_TRACKS_MANY_CODECS = 2
)

// The types of ModEx data (E-RTMP v2, VideoPacketModExType and AudioPacketModExType).
const (
	MODEX_TIMESTAMP_OFFSET_NANO = 0
)

var (
	FOURCC_AVC  = [4]byte{'a', 'v', 'c', '1'}
	FOURCC_AV1  = [4]byte{'a', 'v', '0', '1'}
	FOURCC_VP9  = [4]byte{'v', 'p', '0', '9'}
	FOURCC_HEVC = [4]byte{'h', 'v', 'c', '1'}
	FOURCC_VVC  = [4]byte{'v', 'v', 'c', '1'}
)

func FourCCToFloat(fourcc [4]byte) float64 {
	i := int(fourcc[0])<<24 | int(fourcc[1])<<16 | int(fourcc[2])<<8 | int(fourcc[3])

	return float64(i)
}

type Tag struct {
	/*
		8 = Audio
		9 = Video
		18 = Script data
	*/
	Type uint8

	/*
		SoundFormat: UB[4]
		0 = Linear PCM, platform endian
		1 = ADPCM
		2 = MP3
		3 = Linear PCM, little endian
		4 = Nellymoser 16-kHz mono
		5 = Nellymoser 8-kHz mono
		6 = Nellymoser
		7 = G.711 A-law logarithmic PCM
		8 = G.711 mu-law logarithmic PCM
		9 = reserved
		10 = AAC
		11 = Speex
		14 = MP3 8-Khz
		15 = Device-specific sound
		Formats 7, 8, 14, and 15 are reserved for internal use
		AAC is supported in Flash Player 9,0,115,0 and higher.
		Speex is supported in Flash Player 10 and higher.
	*/
	SoundFormat uint8

	/*
		SoundRate: UB[2]
		Sampling rate
		0 = 5.5-kHz For AAC: always 3
		1 = 11-kHz
		2 = 22-kHz
		3 = 44-kHz
	*/
	SoundRate uint8

	/*
		SoundSize: UB[1]
		0 = snd8Bit
		1 = snd16Bit
		Size of each sample.
		This parameter only pertains to uncompressed formats.
		Compressed formats always decode to 16 bits internally
	*/
	SoundSize uint8

	/*
		SoundType: UB[1]
		0 = sndMono
		1 = sndStereo
		Mono or stereo sound For Nellymoser: always 0
		For AAC: always 1
	*/
	SoundType uint8

	/*
		0: AAC sequence header
		1: AAC raw
	*/
	AACPacketType uint8

	/*
		0: reserved
		1: keyframe (for AVC, a seekable frame)
		2: inter frame (for AVC, a non- seekable frame)
		3: disposable inter frame (H.263 only)
		4: generated keyframe (reserved for server use only)
		5: video info/command frame
		6: reserved
		7: reserved
	*/
	FrameType uint8

	/*
		Video: FrameType & 0b1000 != 0 (ExVideoTagHeader)
		Audio: SoundFormat == 9 (ExAudioTagHeader)
	*/
	IsExHeader bool

	/*
		1: JPEG (currently unused)
		2: Sorenson H.263
		3: Screen video
		4: On2 VP6
		5: On2 VP6 with alpha channel
		6: Screen video version 2
		7: AVC
	*/
	CodecID uint8

	/*
		0: PacketTypeSequenceStart
		1: PacketTypeCodedFrames
		2: PacketTypeSequenceEnd
		3: PacketTypeCodedFramesX
		4: PacketTypeMetadata
		5: PacketTypeMPEG2TSSequenceStart
		6: PacketTypeMultitrack
		7: PacketTypeModEx (only while parsing, the packet type after the ModEx data is stored)
	*/
	PacketType uint8

	/*
		The nanosecond offset to the timestamp of the packet from a ModEx TimestampOffsetNano (E-RTMP v2),
		0..999999. It is not written.
	*/
	TimestampNanoOffset uint32

	/*
		A video or audio packet of the type Multitrack (E-RTMP v2). PacketType is the packet type of the
		tracks, TrackID the id of the track. A message with several tracks (MultitrackType ManyTracks or
		ManyTracksManyCodecs) has all of them in Data after ParseHeader, SplitTracks returns a tag for each.
		FillHeader writes a OneTrack header for the track.
	*/
	IsMultitrack   bool
	MultitrackType uint8
	TrackID        uint8

	/*
		0: AVC sequence header
		1: AVC NALU
		2: AVC end of sequence (lower level NALU sequence ender is not required or supported)
	*/
	AVCPacketType uint8

	CompositionTime int64

	FourCC [4]byte

	Data []byte
}

func (t Tag) ChannelLayout() av.ChannelLayout {
	if t.SoundType == SOUND_MONO {
		return av.CH_MONO
	} else {
		return av.CH_STEREO
	}
}

// The channel orders of a MultichannelConfig (E-RTMP v2, AudioChannelOrder).
const (
	AUDIO_CHANNEL_ORDER_UNSPECIFIED = 0
	AUDIO_CHANNEL_ORDER_NATIVE      = 1
	AUDIO_CHANNEL_ORDER_CUSTOM      = 2
)

// MultichannelConfig is the data of an audio packet of the type MultichannelConfig (E-RTMP v2).
type MultichannelConfig struct {
	ChannelOrder uint8
	ChannelCount uint8

	// the speaker (AudioChannel) of each channel, with AUDIO_CHANNEL_ORDER_CUSTOM
	ChannelMapping []uint8

	// the present channels (AudioChannelMask), with AUDIO_CHANNEL_ORDER_NATIVE
	ChannelFlags uint32
}

// ParseMultichannelConfig parses the data of an audio packet of the type MultichannelConfig (E-RTMP v2).
func ParseMultichannelConfig(data []byte) (config MultichannelConfig, err error) {
	if len(data) < 2 {
		err = fmt.Errorf("audiodata: multichannel config invalid: not enough bytes")
		return
	}
	config.ChannelOrder = data[0]
	config.ChannelCount = data[1]

	switch config.ChannelOrder {
	case AUDIO_CHANNEL_ORDER_CUSTOM:
		if len(data) < 2+int(config.ChannelCount) {
			err = fmt.Errorf("audiodata: multichannel config invalid: not enough bytes for the channel mapping")
			return
		}
		config.ChannelMapping = append([]uint8(nil), data[2:2+int(config.ChannelCount)]...)

	case AUDIO_CHANNEL_ORDER_NATIVE:
		if len(data) < 6 {
			err = fmt.Errorf("audiodata: multichannel config invalid: not enough bytes for the channel flags")
			return
		}
		config.ChannelFlags = pio.U32BE(data[2:])
	}

	return
}

func (t *Tag) audioParseHeader(b []byte) (n int, err error) {
	if len(b) < n+1 {
		err = fmt.Errorf("audiodata: parse invalid")
		return
	}

	flags := b[n]
	n++
	t.SoundFormat = flags >> 4

	if t.SoundFormat == SOUND_EXHEADER {
		// ExAudioTagHeader (E-RTMP v2): the lower 4 bits are the AudioPacketType
		t.IsExHeader = true
		t.PacketType = flags & 0x0f

		if n, err = t.parseModEx(b, n); err != nil {
			err = fmt.Errorf("audiodata: %w", err)
			return
		}

		if n, err = t.parseMultitrack(b, n, AUDIO_PKTTYPE_MULTITRACK); err != nil {
			err = fmt.Errorf("audiodata: %w", err)
		}
		return
	}

	t.SoundRate = (flags >> 2) & 0x3
	t.SoundSize = (flags >> 1) & 0x1
	t.SoundType = flags & 0x1

	switch t.SoundFormat {
	case SOUND_AAC:
		if len(b) < n+1 {
			err = fmt.Errorf("audiodata: parse invalid")
			return
		}
		t.AACPacketType = b[n]
		n++
	}

	return
}

func (t Tag) audioFillHeader(b []byte) (n int) {
	if t.IsExHeader {
		if t.IsMultitrack {
			b[n] = SOUND_EXHEADER<<4 | AUDIO_PKTTYPE_MULTITRACK
			n++
			b[n] = MULTITRACK_ONE_TRACK<<4 | t.PacketType&0x0f
			n++
		} else {
			b[n] = SOUND_EXHEADER<<4 | t.PacketType&0x0f
			n++
		}
		copy(b[n:], t.FourCC[:])
		n += 4
		if t.IsMultitrack {
			b[n] = t.TrackID
			n++
		}
		return
	}

	var flags uint8
	flags |= t.SoundFormat << 4
	flags |= t.SoundRate << 2
	flags |= t.SoundSize << 1
	flags |= t.SoundType
	b[n] = flags
	n++

	switch t.SoundFormat {
	case SOUND_AAC:
		b[n] = t.AACPacketType
		n++
	}

	return
}

// parseModEx parses the ModEx data (E-RTMP v2) after the packet type, if the packet type is ModEx, and sets
// the packet type that follows it. The data of a TimestampOffsetNano is stored in TimestampNanoOffset, the
// data of other types is skipped.
func (t *Tag) parseModEx(b []byte, n int) (int, error) {
	for t.PacketType == PKTTYPE_MODEX {
		if len(b) < n+1 {
			return n, fmt.Errorf("parse invalid: not enough bytes for the ModEx data size")
		}
		size := int(b[n]) + 1
		n++
		if size == 256 {
			if len(b) < n+2 {
				return n, fmt.Errorf("parse invalid: not enough bytes for the ModEx data size")
			}
			size = int(pio.U16BE(b[n:])) + 1
			n += 2
		}

		// the data and the byte with the ModEx type and the next packet type
		if len(b) < n+size+1 {
			return n, fmt.Errorf("parse invalid: not enough bytes for the ModEx data")
		}
		data := b[n : n+size]
		n += size

		modExType := b[n] >> 4
		t.PacketType = b[n] & 0x0f
		n++

		if modExType == MODEX_TIMESTAMP_OFFSET_NANO && len(data) >= 3 {
			t.TimestampNanoOffset = pio.U24BE(data)
		}
	}

	return n, nil
}

// parseMultitrack parses the multitrack header (E-RTMP v2) if the packet type is multitrackPacketType, the
// FourCC and, with OneTrack, the track id. With several tracks in the message, it stops in front of the
// first track.
func (t *Tag) parseMultitrack(b []byte, n int, multitrackPacketType uint8) (int, error) {
	if t.PacketType == multitrackPacketType {
		if len(b) < n+1 {
			return n, fmt.Errorf("parse invalid: not enough bytes for the multitrack type")
		}
		t.IsMultitrack = true
		t.MultitrackType = b[n] >> 4
		t.PacketType = b[n] & 0x0f
		n++

		if t.MultitrackType > MULTITRACK_MANY_TRACKS_MANY_CODECS {
			return n, fmt.Errorf("parse invalid: unknown multitrack type %d", t.MultitrackType)
		}
		if t.PacketType == multitrackPacketType {
			return n, fmt.Errorf("parse invalid: nested multitrack")
		}
		if t.MultitrackType == MULTITRACK_MANY_TRACKS_MANY_CODECS {
			// each track has its FourCC
			return n, nil
		}
	}

	if len(b) < n+4 {
		return n, fmt.Errorf("parse invalid: not enough bytes for the fourCC value")
	}
	copy(t.FourCC[:], b[n:n+4])
	n += 4

	if t.IsMultitrack {
		if t.MultitrackType != MULTITRACK_ONE_TRACK {
			// each track has its id and size
			return n, nil
		}
		if len(b) < n+1 {
			return n, fmt.Errorf("parse invalid: not enough bytes for the track id")
		}
		t.TrackID = b[n]
		n++
	}

	return n, nil
}

// SplitTracks returns a tag for each track of a multitrack message (E-RTMP v2) with several tracks, as
// parsed by ParseHeader with the tracks in Data. Any other tag is returned as it is.
func SplitTracks(tag Tag) ([]Tag, error) {
	if !tag.IsMultitrack || tag.MultitrackType == MULTITRACK_ONE_TRACK {
		return []Tag{tag}, nil
	}

	tags := []Tag{}
	data := tag.Data
	for len(data) != 0 {
		track := tag
		n := 0
		if tag.MultitrackType == MULTITRACK_MANY_TRACKS_MANY_CODECS {
			if len(data) < n+4 {
				return nil, fmt.Errorf("flvio: multitrack invalid: not enough bytes for the fourCC value")
			}
			copy(track.FourCC[:], data[n:n+4])
			n += 4
		}
		if len(data) < n+4 {
			return nil, fmt.Errorf("flvio: multitrack invalid: not enough bytes for the track id and size")
		}
		track.TrackID = data[n]
		size := int(pio.U24BE(data[n+1:]))
		n += 4
		if len(data) < n+size {
			return nil, fmt.Errorf("flvio: multitrack invalid: not enough bytes for the track")
		}
		body := data[n : n+size]
		data = data[n+size:]

		if track.Type == TAG_VIDEO {
			m, err := track.videoParseCodecHeader(body, 0)
			if err != nil {
				return nil, fmt.Errorf("flvio: multitrack invalid: %w", err)
			}
			body = body[m:]
		}
		track.Data = body
		tags = append(tags, track)
	}

	return tags, nil
}

// hasCompositionTime returns whether a video packet of the ExVideoTagHeader has a composition time offset
// (E-RTMP v2: CodedFrames of AVC, HEVC and VVC).
func (t Tag) hasCompositionTime() bool {
	return t.PacketType == PKTTYPE_CODED_FRAMES && (t.FourCC == FOURCC_AVC || t.FourCC == FOURCC_HEVC || t.FourCC == FOURCC_VVC)
}

// videoParseCodecHeader parses the fields of the ExVideoTagHeader after the FourCC (and the track id) that
// depend on the codec, i.e. the composition time offset.
func (t *Tag) videoParseCodecHeader(b []byte, n int) (int, error) {
	t.CompositionTime = 0
	if t.hasCompositionTime() {
		if len(b) < n+3 {
			return n, fmt.Errorf("parse invalid: not enough bytes for the composition time")
		}
		t.CompositionTime = int64(pio.I24BE(b[n:]))
		n += 3
	}
	return n, nil
}

func (t *Tag) videoParseHeader(b []byte) (n int, err error) {
	if len(b) < n+1 {
		err = fmt.Errorf("videodata: parse invalid")
		return
	}
	flags := b[n]
	t.FrameType = flags >> 4
	t.CodecID = flags & 0b1111

	//fmt.Printf("%#8b\n", flags)
	n++

	if (t.FrameType & 0b1000) != 0 {
		t.IsExHeader = true
		t.PacketType = t.CodecID
		t.CodecID = 0

		if n, err = t.parseModEx(b, n); err != nil {
			err = fmt.Errorf("videodata: %w", err)
			return
		}

		// the frame type is to be ignored for metadata (E-RTMP v1)
		if t.PacketType != PKTTYPE_METADATA {
			t.FrameType = t.FrameType & 0b0111
		}

		if t.PacketType != PKTTYPE_METADATA && t.FrameType == FRAME_COMMAND {
			// a command frame has a command (UI8) instead of a FourCC and no video data (E-RTMP v2), the
			// command is the first byte of the data
			if len(b) < n+1 {
				err = fmt.Errorf("videodata: parse invalid: not enough bytes for the video command")
			}
			return
		}
	}

	if !t.IsExHeader {
		if t.CodecID != VIDEO_H264 {
			// other legacy codecs (e.g. Sorenson H.263, VP6) have no further header fields here
			t.PacketType = PKTTYPE_CODED_FRAMES
		} else if t.FrameType == FRAME_INTER || t.FrameType == FRAME_KEY {
			if len(b) < n+4 {
				err = fmt.Errorf("videodata: parse invalid: not enough bytes for the AVC header")
				return
			}
			t.AVCPacketType = b[n]
			switch t.AVCPacketType {
			case AVC_SEQHDR:
				t.PacketType = PKTTYPE_SEQUENCE_START
			case AVC_NALU:
				t.PacketType = PKTTYPE_CODED_FRAMES
			case AVC_EOS:
				t.PacketType = PKTTYPE_SEQUENCE_END
			}
			n++

			t.CompositionTime = int64(pio.I24BE(b[n:]))
			n += 3
		}
	} else {
		if n, err = t.parseMultitrack(b, n, PKTTYPE_MULTITRACK); err != nil {
			err = fmt.Errorf("videodata: %w", err)
			return
		}

		if t.IsMultitrack && t.MultitrackType != MULTITRACK_ONE_TRACK {
			// the tracks are in the data (SplitTracks)
			return
		}

		// AVC, HEVC and VVC have a composition time offset with CodedFrames (E-RTMP v2)
		if n, err = t.videoParseCodecHeader(b, n); err != nil {
			err = fmt.Errorf("videodata: %w", err)
			return
		}
	}

	//fmt.Printf("parseVideoHeader: PacketType: %d\n", t.PacketType)

	return
}

// ParseColorInfo parses the data of a video packet of the type PKTTYPE_METADATA (E-RTMP), which is a
// series of AMF encoded [name, value] pairs. It returns the value of the last "colorInfo" pair, or nil if
// that pair resets the color info (undefined, null or an empty object). ok is false if there's no such pair.
func ParseColorInfo(b []byte) (colorInfo AMFMap, ok bool) {
	for len(b) > 0 {
		name, n, err := ParseAMF0Val(b)
		if err != nil {
			return
		}
		b = b[n:]

		val, n, err := ParseAMF0Val(b)
		if err != nil {
			return
		}
		b = b[n:]

		if name, isString := name.(string); !isString || name != "colorInfo" {
			continue
		}

		switch val := val.(type) {
		case AMFMap:
			colorInfo, ok = val, true
			if len(val) == 0 {
				colorInfo = nil
			}
		case nil:
			colorInfo, ok = nil, true
		}
	}

	return
}

// ColorInfoData returns the data of a video packet of the type PKTTYPE_METADATA (E-RTMP) with the color
// info. A nil color info resets it, it is written as undefined.
func ColorInfoData(colorInfo AMFMap) []byte {
	if colorInfo == nil {
		b := make([]byte, LenAMF0Val("colorInfo")+1)
		n := FillAMF0Val(b, "colorInfo")
		b[n] = undefinedmarker
		return b
	}

	b := make([]byte, LenAMF0Val("colorInfo")+LenAMF0Val(colorInfo))
	n := FillAMF0Val(b, "colorInfo")
	FillAMF0Val(b[n:], colorInfo)
	return b
}

// compositionTimeSI24 returns the composition time limited to the range of an SI24.
func compositionTimeSI24(cts int64) int32 {
	if cts > 0x7FFFFF {
		return 0x7FFFFF
	}
	if cts < -0x800000 {
		return -0x800000
	}
	return int32(cts)
}

func (t Tag) videoFillHeader(b []byte) (n int) {
	if t.IsExHeader {
		if t.PacketType != PKTTYPE_METADATA && t.FrameType == FRAME_COMMAND {
			// the command is the first byte of the data
			b[n] = t.FrameType<<4 | t.PacketType | 0b10000000
			n++
			return
		}
		if t.IsMultitrack {
			b[n] = t.FrameType<<4 | PKTTYPE_MULTITRACK | 0b10000000
			n++
			b[n] = MULTITRACK_ONE_TRACK<<4 | t.PacketType&0x0f
			n++
		} else {
			b[n] = t.FrameType<<4 | t.PacketType | 0b10000000
			n++
		}
		copy(b[n:], t.FourCC[:])
		n += 4
		if t.IsMultitrack {
			b[n] = t.TrackID
			n++
		}

		// AVC, HEVC and VVC have a composition time offset with CodedFrames (E-RTMP v2)
		if t.hasCompositionTime() {
			pio.PutI24BE(b[n:], compositionTimeSI24(t.CompositionTime))
			n += 3
		}
	} else {
		flags := t.FrameType<<4 | t.CodecID
		b[n] = flags
		n++
		if t.CodecID == VIDEO_H264 {
			b[n] = t.AVCPacketType
			n++
			pio.PutI24BE(b[n:], compositionTimeSI24(t.CompositionTime))
			n += 3
		}
	}

	//fmt.Printf("videoFillHeader: PacketType: %d\n%s\n", t.PacketType, hex.Dump(b[:n]))

	return
}

func (t Tag) FillHeader(b []byte) (n int) {
	switch t.Type {
	case TAG_AUDIO:
		return t.audioFillHeader(b)

	case TAG_VIDEO:
		return t.videoFillHeader(b)
	}

	return
}

func (t *Tag) ParseHeader(b []byte) (n int, err error) {
	switch t.Type {
	case TAG_AUDIO:
		return t.audioParseHeader(b)

	case TAG_VIDEO:
		return t.videoParseHeader(b)
	}

	return
}

const (
	// TypeFlagsReserved UB[5]
	// TypeFlagsAudio    UB[1] Audio tags are present
	// TypeFlagsReserved UB[1] Must be 0
	// TypeFlagsVideo    UB[1] Video tags are present
	FILE_HAS_AUDIO = 0x4
	FILE_HAS_VIDEO = 0x1
)

const TagHeaderLength = 11
const TagTrailerLength = 4

func ParseTagHeader(b []byte) (tag Tag, ts int64, datalen int, err error) {
	tagtype := b[0]

	switch tagtype {
	case TAG_AUDIO, TAG_VIDEO, TAG_SCRIPTDATA:
		tag = Tag{Type: tagtype}

	default:
		err = fmt.Errorf("flvio: ReadTag tagtype=%d invalid", tagtype)
		return
	}

	datalen = int(pio.U24BE(b[1:4]))

	var tslo uint32
	var tshi uint8
	tslo = pio.U24BE(b[4:7])
	tshi = b[7]
	ts = int64(tslo | uint32(tshi)<<24)

	return
}

func ReadTag(r io.Reader, b []byte) (tag Tag, ts int64, err error) {
	if _, err = io.ReadFull(r, b[:TagHeaderLength]); err != nil {
		return
	}
	var datalen int
	if tag, ts, datalen, err = ParseTagHeader(b); err != nil {
		return
	}

	data := make([]byte, datalen)
	if _, err = io.ReadFull(r, data); err != nil {
		return
	}

	var n int
	if n, err = (&tag).ParseHeader(data); err != nil {
		return
	}
	tag.Data = data[n:]

	if _, err = io.ReadFull(r, b[:4]); err != nil {
		return
	}
	return
}

func FillTagHeader(b []byte, tagtype uint8, datalen int, ts int64) (n int) {
	b[n] = tagtype
	n++
	pio.PutU24BE(b[n:], uint32(datalen))
	n += 3
	pio.PutU24BE(b[n:], uint32(ts&0xffffff))
	n += 3
	b[n] = uint8(ts >> 24)
	n++
	pio.PutI24BE(b[n:], 0)
	n += 3
	return
}

func FillTagTrailer(b []byte, datalen int) (n int) {
	pio.PutU32BE(b[n:], uint32(datalen+TagHeaderLength))
	n += 4
	return
}

func WriteTag(w io.Writer, tag Tag, ts int64, b []byte) (err error) {
	data := tag.Data

	n := tag.FillHeader(b[TagHeaderLength:])
	datalen := len(data) + n

	n += FillTagHeader(b, tag.Type, datalen, ts)

	if _, err = w.Write(b[:n]); err != nil {
		return
	}

	if _, err = w.Write(data); err != nil {
		return
	}

	n = FillTagTrailer(b, datalen)
	if _, err = w.Write(b[:n]); err != nil {
		return
	}

	return
}

const FileHeaderLength = 9

func FillFileHeader(b []byte, flags uint8) (n int) {
	// 'FLV', version 1
	pio.PutU32BE(b[n:], 0x464c5601)
	n += 4

	b[n] = flags
	n++

	// DataOffset: UI32 Offset in bytes from start of file to start of body (that is, size of header)
	// The DataOffset field usually has a value of 9 for FLV version 1.
	pio.PutU32BE(b[n:], 9)
	n += 4

	// PreviousTagSize0: UI32 Always 0
	pio.PutU32BE(b[n:], 0)
	n += 4

	return
}

func ParseFileHeader(b []byte) (flags uint8, skip int, err error) {
	flv := pio.U24BE(b[0:3])
	if flv != 0x464c56 { // 'FLV'
		err = fmt.Errorf("flvio: file header cc3 invalid")
		return
	}

	flags = b[4]

	skip = int(pio.U32BE(b[5:9])) - 9 + 4
	if skip < 0 {
		err = fmt.Errorf("flvio: file header datasize invalid")
		return
	}

	return
}
