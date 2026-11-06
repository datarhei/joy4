package flv

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
	"math"
	"sort"
	"strconv"

	"github.com/datarhei/joy4/av"
	"github.com/datarhei/joy4/av/avutil"
	"github.com/datarhei/joy4/codec"
	"github.com/datarhei/joy4/codec/aacparser"
	"github.com/datarhei/joy4/codec/ac3parser"
	"github.com/datarhei/joy4/codec/av1parser"
	"github.com/datarhei/joy4/codec/flacparser"
	"github.com/datarhei/joy4/codec/h264parser"
	"github.com/datarhei/joy4/codec/hevcparser"
	"github.com/datarhei/joy4/codec/mp3parser"
	"github.com/datarhei/joy4/codec/opusparser"
	"github.com/datarhei/joy4/codec/vp9parser"
	"github.com/datarhei/joy4/codec/vvcparser"
	"github.com/datarhei/joy4/format/flv/flvio"
	"github.com/datarhei/joy4/utils/bits/pio"
)

var MaxProbePacketCount = 20

// NewMetadataByStreams returns the metadata (onMetaData) of the streams. The first video and audio stream
// are the default tracks and described by the top-level fields, the other ones by the videoTrackIdInfoMap
// and audioTrackIdInfoMap (E-RTMP v2) with their track ids (see TrackIDs).
func NewMetadataByStreams(streams []av.CodecData) (metadata flvio.AMFMap, err error) {
	metadata = flvio.AMFMap{}

	var trackIDs []uint8
	if trackIDs, err = TrackIDs(streams); err != nil {
		return
	}

	videoTracks := flvio.AMFMap{}
	audioTracks := flvio.AMFMap{}

	for i, _stream := range streams {
		typ := _stream.Type()
		trackID := trackIDs[i]
		switch {
		case typ.IsVideo():
			stream := _stream.(av.VideoCodecData)
			info := flvio.AMFMap{}
			switch typ {
			case av.H264:
				if trackID == 0 {
					info["videocodecid"] = flvio.VIDEO_H264
				} else {
					info["videocodecid"] = flvio.FourCCToFloat(flvio.FOURCC_AVC)
				}
			case av.HEVC:
				info["videocodecid"] = flvio.FourCCToFloat(flvio.FOURCC_HEVC)
			case av.VP9:
				info["videocodecid"] = flvio.FourCCToFloat(flvio.FOURCC_VP9)
			case av.AV1:
				info["videocodecid"] = flvio.FourCCToFloat(flvio.FOURCC_AV1)
			case av.VVC:
				info["videocodecid"] = flvio.FourCCToFloat(flvio.FOURCC_VVC)

			default:
				err = fmt.Errorf("flv: metadata: unsupported video codecType=%v", stream.Type())
				return
			}

			width, height := stream.Width(), stream.Height()

			if width != 0 {
				info["width"] = width
			}

			if height != 0 {
				info["height"] = height
			}

			if trackID != 0 {
				videoTracks[strconv.Itoa(int(trackID))] = info
				break
			}

			for key, value := range info {
				metadata[key] = value
			}
			if width != 0 {
				metadata["displayWidth"] = width
			}
			if height != 0 {
				metadata["displayHeight"] = height
			}

		case typ.IsAudio():
			stream := _stream.(av.AudioCodecData)
			if trackID != 0 {
				// only codecs with a FourCC (see TrackIDs)
				fourCC, _ := audioFourCC(typ)
				info := flvio.AMFMap{
					"audiocodecid": flvio.FourCCToFloat(fourCC),
					"samplerate":   stream.SampleRate(),
					"channels":     stream.ChannelLayout().Count(),
				}
				audioTracks[strconv.Itoa(int(trackID))] = info
				break
			}

			switch typ {
			case av.AAC:
				metadata["audiocodecid"] = flvio.SOUND_AAC

			case av.SPEEX:
				metadata["audiocodecid"] = flvio.SOUND_SPEEX

			case av.NELLYMOSER:
				metadata["audiocodecid"] = int(nellymoserTag(stream).SoundFormat)

			case av.MP3:
				metadata["audiocodecid"] = int(mp3Tag(stream).SoundFormat)

			case av.OPUS:
				metadata["audiocodecid"] = flvio.FourCCToFloat(flvio.FOURCC_OPUS)

			case av.FLAC:
				metadata["audiocodecid"] = flvio.FourCCToFloat(flvio.FOURCC_FLAC)

			case av.AC3:
				metadata["audiocodecid"] = flvio.FourCCToFloat(flvio.FOURCC_AC3)

			case av.EAC3:
				metadata["audiocodecid"] = flvio.FourCCToFloat(flvio.FOURCC_EAC3)

			default:
				err = fmt.Errorf("flv: metadata: unsupported audio codecType=%v", stream.Type())
				return
			}

			metadata["audiosamplerate"] = stream.SampleRate()
		}
	}

	if len(videoTracks) != 0 {
		metadata["videoTrackIdInfoMap"] = videoTracks
	}
	if len(audioTracks) != 0 {
		metadata["audioTrackIdInfoMap"] = audioTracks
	}

	return
}

// TrackIDs returns the track id (E-RTMP v2) of each stream: the n-th video stream has the track id n, the
// n-th audio stream as well. Streams with a track id other than 0 are written as multitrack (OneTrack), so
// their codec must have a FourCC (H.264, HEVC, VP9, AV1, VVC, AAC, Opus, FLAC, AC-3, E-AC-3, MP3), otherwise an error is returned. There
// are at most 256 tracks per media type.
func TrackIDs(streams []av.CodecData) ([]uint8, error) {
	trackIDs := make([]uint8, len(streams))
	videoTracks, audioTracks := 0, 0
	for i, stream := range streams {
		var n int
		switch {
		case stream.Type().IsVideo():
			n = videoTracks
			videoTracks++
		case stream.Type().IsAudio():
			n = audioTracks
			audioTracks++
		}
		if n > 255 {
			return nil, fmt.Errorf("flv: too many tracks")
		}
		if n != 0 {
			switch stream.Type() {
			case av.H264, av.HEVC, av.VP9, av.AV1, av.VVC, av.AAC, av.OPUS, av.FLAC, av.AC3, av.EAC3, av.MP3:
			default:
				return nil, fmt.Errorf("flv: codecType=%v can't be written as track %d", stream.Type(), n)
			}
		}
		trackIDs[i] = uint8(n)
	}
	return trackIDs, nil
}

// audioFourCC returns the FourCC of the ExAudioTagHeader (E-RTMP v2) of an audio codec.
func audioFourCC(typ av.CodecType) ([4]byte, bool) {
	switch typ {
	case av.AAC:
		return flvio.FOURCC_AAC, true
	case av.OPUS:
		return flvio.FOURCC_OPUS, true
	case av.FLAC:
		return flvio.FOURCC_FLAC, true
	case av.AC3:
		return flvio.FOURCC_AC3, true
	case av.EAC3:
		return flvio.FOURCC_EAC3, true
	case av.MP3:
		return flvio.FOURCC_MP3, true
	}
	return [4]byte{}, false
}

// TrackCodecDataTags returns the tags that start the track of the stream (see CodecDataToTag and TrackTag): its
// sequence start, if it has one, and, as FFmpeg writes it, a multichannel config with the channel layout
// after the sequence start of the ExAudioTagHeader. FFmpeg's FLV demuxer creates the stream of an audio track
// with an empty sequence start (e.g. AC-3) only at its multichannel config or its first frame, i.e. without
// one, the order of FFmpeg's streams wouldn't be the one of the tracks. MP3 has no sequence start, but as a
// track from 1 on (ExAudioTagHeader) it gets an empty one, as FFmpeg writes it.
func TrackCodecDataTags(stream av.CodecData, trackID uint8) (tags []flvio.Tag, err error) {
	tag, ok, err := CodecDataToTag(stream)
	if err != nil {
		return
	}
	if !ok && trackID != 0 && stream.Type() == av.MP3 {
		tag = flvio.Tag{
			Type:        flvio.TAG_AUDIO,
			SoundFormat: flvio.SOUND_EXHEADER,
			IsExHeader:  true,
			PacketType:  flvio.PKTTYPE_SEQUENCE_START,
			FourCC:      flvio.FOURCC_MP3,
		}
		ok = true
	}
	if !ok {
		return
	}

	tag = TrackTag(tag, trackID)
	tags = append(tags, tag)

	if tag.Type == flvio.TAG_AUDIO && tag.IsExHeader {
		layout := stream.(av.AudioCodecData).ChannelLayout()
		config := flvio.MultichannelConfig{
			ChannelOrder: flvio.AUDIO_CHANNEL_ORDER_NATIVE,
			ChannelCount: uint8(layout.Count()),
			ChannelFlags: channelFlags(layout),
		}
		tag.PacketType = flvio.AUDIO_PKTTYPE_MULTICHANNEL_CONFIG
		tag.Data = config.Bytes()
		tags = append(tags, tag)
	}

	return
}

// channelFlags returns the channel flags (AudioChannelMask) of a MultichannelConfig (E-RTMP v2) of the channel
// layout.
func channelFlags(layout av.ChannelLayout) uint32 {
	var flags uint32
	for _, c := range []struct {
		channel av.ChannelLayout
		flag    uint32
	}{
		{av.CH_FRONT_LEFT, flvio.AUDIO_CHANNEL_MASK_FRONT_LEFT},
		{av.CH_FRONT_RIGHT, flvio.AUDIO_CHANNEL_MASK_FRONT_RIGHT},
		{av.CH_FRONT_CENTER, flvio.AUDIO_CHANNEL_MASK_FRONT_CENTER},
		{av.CH_LOW_FREQ, flvio.AUDIO_CHANNEL_MASK_LOW_FREQUENCY1},
		{av.CH_BACK_LEFT, flvio.AUDIO_CHANNEL_MASK_BACK_LEFT},
		{av.CH_BACK_RIGHT, flvio.AUDIO_CHANNEL_MASK_BACK_RIGHT},
		{av.CH_BACK_CENTER, flvio.AUDIO_CHANNEL_MASK_BACK_CENTER},
		{av.CH_SIDE_LEFT, flvio.AUDIO_CHANNEL_MASK_SIDE_LEFT},
		{av.CH_SIDE_RIGHT, flvio.AUDIO_CHANNEL_MASK_SIDE_RIGHT},
	} {
		if layout&c.channel != 0 {
			flags |= c.flag
		}
	}
	return flags
}

// TrackTag returns the tag as a tag of the track (E-RTMP v2 multitrack, OneTrack). The tag of the track 0 is
// returned as it is. A legacy H.264, AAC or MP3 tag is converted to the ExVideoTagHeader or ExAudioTagHeader.
func TrackTag(tag flvio.Tag, trackID uint8) flvio.Tag {
	if trackID == 0 {
		return tag
	}

	if !tag.IsExHeader {
		switch tag.Type {
		case flvio.TAG_VIDEO:
			// see TrackIDs, only H.264 has a legacy header
			tag.IsExHeader = true
			tag.FourCC = flvio.FOURCC_AVC
			switch tag.AVCPacketType {
			case flvio.AVC_SEQHDR:
				tag.PacketType = flvio.PKTTYPE_SEQUENCE_START
			case flvio.AVC_NALU:
				tag.PacketType = flvio.PKTTYPE_CODED_FRAMES
			case flvio.AVC_EOS:
				tag.PacketType = flvio.PKTTYPE_SEQUENCE_END
			}
		case flvio.TAG_AUDIO:
			// see TrackIDs, only AAC and MP3 have a legacy header
			if tag.SoundFormat == flvio.SOUND_MP3 || tag.SoundFormat == flvio.SOUND_MP3_8KHZ {
				tag.FourCC = flvio.FOURCC_MP3
				tag.PacketType = flvio.PKTTYPE_CODED_FRAMES
			} else {
				tag.FourCC = flvio.FOURCC_AAC
				switch tag.AACPacketType {
				case flvio.AAC_SEQHDR:
					tag.PacketType = flvio.PKTTYPE_SEQUENCE_START
				case flvio.AAC_RAW:
					tag.PacketType = flvio.PKTTYPE_CODED_FRAMES
				}
			}
			tag.IsExHeader = true
			tag.SoundFormat = flvio.SOUND_EXHEADER
		}
	}

	tag.IsMultitrack = true
	tag.MultitrackType = flvio.MULTITRACK_ONE_TRACK
	tag.TrackID = trackID
	return tag
}

// Prober probes the streams of FLV tags. Each track of a multitrack stream (E-RTMP v2) becomes a stream of
// its own. The streams of the tracks of a media type are sorted by their track id once probing is done.
// HasAudio and HasVideo are about the default track (0), the other tracks are announced by PushMetadata
// (audioTrackIdInfoMap, videoTrackIdInfoMap). GotAudio and GotVideo, AudioStreamIdx and VideoStreamIdx are
// about the first audio and video stream.
type Prober struct {
	HasAudio, HasVideo             bool
	GotAudio, GotVideo             bool
	VideoStreamIdx, AudioStreamIdx int
	PushedCount                    int
	MaxProbePacketCount            int
	Streams                        []av.CodecData
	CachedPkts                     []av.Packet

	// ColorInfo is the color info (HDR metadata, E-RTMP) of the first video stream that applies to its
	// packet returned last and the following ones. It is nil if no color info has been received or it has
	// been reset. The color info of other video tracks is ignored.
	ColorInfo flvio.AMFMap

	// the tracks with a stream, by track id
	videoTracks map[uint8]*probedTrack
	audioTracks map[uint8]*probedTrack

	// the tracks (from 1 on) announced by the metadata
	announcedVideoTracks map[uint8]bool
	announcedAudioTracks map[uint8]bool
	hasTrackInfo         bool

	// the audio tracks with a sequence start or multichannel config, but without a stream yet, because the
	// config is in the first frame (AC-3, E-AC-3, Opus without an ID header)
	pendingAudioTracks map[uint8]bool

	// whether a coded frame (of any track or codec) has been pushed
	gotCodedFrame bool

	// whether the streams have been sorted by track id after probing
	sorted bool

	// the tags received while probing, converted to CachedPkts once probing is done
	cachedTags []cachedTag

	// the color info that has been received before the packet with the same index in CachedPkts, and
	// before the next packet after CachedPkts
	cachedColorInfo  []*colorInfoUpdate
	pendingColorInfo *colorInfoUpdate

	// the last error of PushTag
	lastErr error
}

// probedTrack is a track with a stream.
type probedTrack struct {
	idx int

	// the config record of a video stream (H.264, HEVC) that applies to the following packets, and its
	// codec data. It differs from the one in Streams after a new sequence header in the middle of the stream.
	videoRecord    []byte
	videoCodecData av.CodecData

	// the parameter sets of videoRecord, length-prefixed as the NALUs in the frames. They are put in front
	// of every key frame as long as the config differs from the one in Streams. It is nil otherwise.
	videoParamSets []byte
}

type colorInfoUpdate struct {
	colorInfo flvio.AMFMap
}

// isVideoMetadata returns whether the tag is a video packet of the type PKTTYPE_METADATA (E-RTMP).
func isVideoMetadata(tag flvio.Tag) bool {
	return tag.Type == flvio.TAG_VIDEO && tag.IsExHeader && tag.PacketType == flvio.PKTTYPE_METADATA
}

type cachedTag struct {
	tag       flvio.Tag
	timestamp int64
}

func NewProber(maxProbePacketCount int) *Prober {
	prober := &Prober{
		MaxProbePacketCount: maxProbePacketCount,
	}

	return prober
}

// CacheTag caches a tag while probing. It is converted to a packet once probing is done, such that it
// gets the index of its stream even if it arrived before the sequence header of its stream.
func (prober *Prober) CacheTag(tag flvio.Tag, timestamp int64) {
	prober.cachedTags = append(prober.cachedTags, cachedTag{tag, timestamp})
}

func (prober *Prober) PushTag(tag flvio.Tag, timestamp int64) (err error) {
	defer func() {
		if err != nil {
			prober.lastErr = err
		}
	}()

	prober.PushedCount++

	if prober.MaxProbePacketCount <= 0 {
		prober.MaxProbePacketCount = MaxProbePacketCount
	}

	if prober.PushedCount > prober.MaxProbePacketCount {
		err = fmt.Errorf("flv: max probe packet count reached")
		return
	}

	switch tag.Type {
	case flvio.TAG_VIDEO:
		if isVideoMetadata(tag) {
			// it applies to the packets after it
			prober.CacheTag(tag, timestamp)
		} else if isVideoConfig(tag) {
			if prober.track(tag) != nil {
				// a new config in the middle of the stream
				prober.CacheTag(tag, timestamp)
				return
			}
			var stream av.CodecData
			if stream, err = videoCodecData(tag); err != nil || stream == nil {
				return
			}
			prober.addTrack(tag, stream)
		} else if isCodedFrame(tag) {
			prober.gotCodedFrame = true
			prober.CacheTag(tag, timestamp)
		}

	case flvio.TAG_AUDIO:
		if prober.track(tag) == nil {
			var stream av.CodecData
			if stream, err = audioCodecData(tag); err != nil {
				return
			}
			if stream != nil {
				prober.addTrack(tag, stream)
			} else if tag.IsExHeader && tagCodecType(tag) != 0 && (tag.PacketType == flvio.PKTTYPE_SEQUENCE_START || tag.PacketType == flvio.AUDIO_PKTTYPE_MULTICHANNEL_CONFIG) {
				// wait for its first frame
				if prober.pendingAudioTracks == nil {
					prober.pendingAudioTracks = map[uint8]bool{}
				}
				prober.pendingAudioTracks[tag.TrackID] = true
			}
		}
		if isCodedFrame(tag) {
			prober.gotCodedFrame = true
			prober.CacheTag(tag, timestamp)
		}
	}

	return
}

// isVideoConfig returns whether the tag is a video sequence start (config record or AV1 video descriptor).
func isVideoConfig(tag flvio.Tag) bool {
	return isVideoSequenceStart(tag) || (tag.Type == flvio.TAG_VIDEO && tag.IsExHeader && tag.PacketType == flvio.PKTTYPE_MPEG2TS_SEQUENCE_START)
}

// isCodedFrame returns whether the tag is an audio or video frame, of any codec.
func isCodedFrame(tag flvio.Tag) bool {
	switch tag.Type {
	case flvio.TAG_VIDEO:
		if tag.IsExHeader || tag.CodecID == flvio.VIDEO_H264 {
			return tag.PacketType == flvio.PKTTYPE_CODED_FRAMES || tag.PacketType == flvio.PKTTYPE_CODED_FRAMESX
		}
		// other legacy codecs
		return true
	case flvio.TAG_AUDIO:
		if tag.IsExHeader {
			return tag.PacketType == flvio.PKTTYPE_CODED_FRAMES
		}
		if tag.SoundFormat == flvio.SOUND_AAC {
			return tag.AACPacketType == flvio.AAC_RAW
		}
		return true
	}
	return false
}

// videoCodecData returns the codec data of a video sequence start, or nil for an unsupported codec.
func videoCodecData(tag flvio.Tag) (av.CodecData, error) {
	switch tagCodecType(tag) {
	case av.H264:
		stream, err := h264parser.NewCodecDataFromAVCDecoderConfRecord(tag.Data)
		if err != nil {
			return nil, fmt.Errorf("flv: h264 seqhdr invalid: %s", err.Error())
		}
		return stream, nil
	case av.HEVC:
		stream, err := hevcparser.NewCodecDataFromHEVCDecoderConfRecord(tag.Data)
		if err != nil {
			return nil, fmt.Errorf("flv: hevc seqhdr invalid: %s", err.Error())
		}
		return stream, nil
	case av.VP9:
		stream, err := vp9parser.NewCodecDataFromVPDecoderConfRecord(tag.Data)
		if err != nil {
			return nil, fmt.Errorf("flv: vp9 seqhdr invalid: %s", err.Error())
		}
		return stream, nil
	case av.AV1:
		if tag.PacketType == flvio.PKTTYPE_MPEG2TS_SEQUENCE_START {
			stream, err := av1parser.NewCodecDataFromAV1VideoDescriptor(tag.Data)
			if err != nil {
				return nil, fmt.Errorf("flv: av1 video descriptor invalid: %s", err.Error())
			}
			return stream, nil
		}
		stream, err := av1parser.NewCodecDataFromAV1DecoderConfRecord(tag.Data)
		if err != nil {
			return nil, fmt.Errorf("flv: av1 seqhdr invalid: %s", err.Error())
		}
		return stream, nil
	case av.VVC:
		stream, err := vvcparser.NewCodecDataFromVVCDecoderConfRecord(tag.Data)
		if err != nil {
			return nil, fmt.Errorf("flv: vvc seqhdr invalid: %s", err.Error())
		}
		return stream, nil
	}
	return nil, nil
}

// audioCodecData returns the codec data of the first audio tag of a track that has the configuration: the
// sequence start of AAC and FLAC, the first frame of AC-3, E-AC-3 and MP3, or any tag of Speex and Nellymoser. It is nil for other tags and unsupported codecs.
func audioCodecData(tag flvio.Tag) (av.CodecData, error) {
	switch tagCodecType(tag) {
	case av.AAC:
		if (tag.IsExHeader && tag.PacketType == flvio.PKTTYPE_SEQUENCE_START) || (!tag.IsExHeader && tag.AACPacketType == flvio.AAC_SEQHDR) {
			stream, err := aacparser.NewCodecDataFromMPEG4AudioConfigBytes(tag.Data)
			if err != nil {
				return nil, fmt.Errorf("flv: aac seqhdr invalid")
			}
			return stream, nil
		}
	case av.OPUS:
		return opusCodecData(tag)
	case av.FLAC:
		if tag.PacketType == flvio.PKTTYPE_SEQUENCE_START {
			stream, err := flacparser.NewCodecDataFromSequenceHeader(tag.Data)
			if err != nil {
				return nil, fmt.Errorf("flv: flac seqhdr invalid: %w", err)
			}
			return stream, nil
		}
	case av.MP3:
		// there is no sequence start, the config is in the header of each frame
		if (tag.IsExHeader && tag.PacketType == flvio.PKTTYPE_CODED_FRAMES || !tag.IsExHeader) && len(tag.Data) != 0 {
			stream, err := mp3parser.NewCodecDataFromFrame(tag.Data)
			if err != nil {
				return nil, fmt.Errorf("flv: mp3: %w", err)
			}
			return stream, nil
		}
	case av.AC3, av.EAC3:
		// the sequence start has no config, it is in the header of each syncframe
		if tag.PacketType == flvio.PKTTYPE_CODED_FRAMES && len(tag.Data) != 0 {
			stream, err := ac3parser.NewCodecDataFromFrame(tag.Data)
			if err != nil {
				return nil, fmt.Errorf("flv: %s: %w", string(tag.FourCC[:]), err)
			}
			if stream.Type() != tagCodecType(tag) {
				return nil, fmt.Errorf("flv: %s: the frame is %v", string(tag.FourCC[:]), stream.Type())
			}
			return stream, nil
		}
	case av.SPEEX:
		return codec.NewSpeexCodecData(16000, tag.ChannelLayout()), nil
	case av.NELLYMOSER:
		sampleRate, channelLayout := nellymoserFormat(tag)
		return codec.NewNellymoserCodecData(sampleRate, channelLayout), nil
	}
	return nil, nil
}

// opusCodecData returns the codec data of an Opus stream from its ID header (SequenceStart), or, without
// one, from the channel count of a MultichannelConfig or the stereo flag of the first packet (E-RTMP v2).
// It is nil for an empty SequenceStart, the next tag has the channels.
func opusCodecData(tag flvio.Tag) (av.CodecData, error) {
	switch tag.PacketType {
	case flvio.PKTTYPE_SEQUENCE_START:
		if len(tag.Data) == 0 {
			return nil, nil
		}
		stream, err := opusparser.NewCodecDataFromOpusHead(tag.Data)
		if err != nil {
			return nil, fmt.Errorf("flv: opus seqhdr invalid: %w", err)
		}
		return stream, nil
	case flvio.AUDIO_PKTTYPE_MULTICHANNEL_CONFIG:
		config, err := flvio.ParseMultichannelConfig(tag.Data)
		if err != nil {
			return nil, fmt.Errorf("flv: opus: %w", err)
		}
		stream, err := opusparser.NewCodecData(int(config.ChannelCount))
		if err != nil {
			return nil, fmt.Errorf("flv: opus: %w", err)
		}
		return stream, nil
	case flvio.PKTTYPE_CODED_FRAMES:
		channels := 1
		if opusparser.IsStereo(tag.Data) {
			channels = 2
		}
		stream, err := opusparser.NewCodecData(channels)
		if err != nil {
			return nil, fmt.Errorf("flv: opus: %w", err)
		}
		return stream, nil
	}
	return nil, nil
}

// track returns the track of an audio or video tag, or nil if it has no stream (yet).
func (prober *Prober) track(tag flvio.Tag) *probedTrack {
	switch tag.Type {
	case flvio.TAG_VIDEO:
		return prober.videoTracks[tag.TrackID]
	case flvio.TAG_AUDIO:
		return prober.audioTracks[tag.TrackID]
	}
	return nil
}

// addTrack adds the stream of the track of the tag.
func (prober *Prober) addTrack(tag flvio.Tag, stream av.CodecData) {
	track := &probedTrack{idx: len(prober.Streams)}
	prober.Streams = append(prober.Streams, stream)

	switch tag.Type {
	case flvio.TAG_VIDEO:
		track.videoRecord = tag.Data
		track.videoCodecData = stream
		if prober.videoTracks == nil {
			prober.videoTracks = map[uint8]*probedTrack{}
		}
		prober.videoTracks[tag.TrackID] = track
		if !prober.GotVideo {
			prober.GotVideo = true
			prober.VideoStreamIdx = track.idx
		}
	case flvio.TAG_AUDIO:
		if prober.audioTracks == nil {
			prober.audioTracks = map[uint8]*probedTrack{}
		}
		prober.audioTracks[tag.TrackID] = track
		if !prober.GotAudio {
			prober.GotAudio = true
			prober.AudioStreamIdx = track.idx
		}
	}
}

// isFirstVideoTrack returns whether the tag belongs to the first video stream.
func (prober *Prober) isFirstVideoTrack(tag flvio.Tag) bool {
	track := prober.videoTracks[tag.TrackID]
	return track != nil && track.idx == prober.VideoStreamIdx
}

// Err returns the last error of PushTag, or nil.
func (prober *Prober) Err() error {
	return prober.lastErr
}

// PushMetadata sets HasVideo and HasAudio from the codec ids in the metadata (onMetaData), if they are
// supported. An unsupported codec is not waited for, otherwise probing would only end after
// MaxProbePacketCount tags.
//
// The additional tracks of a multitrack stream (E-RTMP v2) in videoTrackIdInfoMap and audioTrackIdInfoMap
// are waited for as well, if their codec (videocodecid, audiocodecid, or the one of the default track) is
// supported. Without these maps, probing ends at the first coded frame after the default tracks, such that
// the sequence starts of the other tracks, sent before the frames, are probed as well.
func (prober *Prober) PushMetadata(metadata flvio.AMFMap) {
	if isSupportedVideoCodecID(metadata["videocodecid"]) {
		prober.HasVideo = true
	}
	if isSupportedAudioCodecID(metadata["audiocodecid"]) {
		prober.HasAudio = true
	}

	if tracks := announcedTracks(metadata["videoTrackIdInfoMap"], "videocodecid", metadata["videocodecid"], isSupportedVideoCodecID); tracks != nil {
		prober.announcedVideoTracks = tracks
		prober.hasTrackInfo = true
	}
	if tracks := announcedTracks(metadata["audioTrackIdInfoMap"], "audiocodecid", metadata["audiocodecid"], isSupportedAudioCodecID); tracks != nil {
		prober.announcedAudioTracks = tracks
		prober.hasTrackInfo = true
	}
}

// announcedTracks returns the ids of the tracks with a supported codec of a TrackIdInfoMap (E-RTMP v2), or
// nil if there's no such map. The codec of a track without its own codec id is the one of the default track.
func announcedTracks(infoMap interface{}, codecIDKey string, defaultCodecID interface{}, isSupported func(interface{}) bool) map[uint8]bool {
	trackInfos, ok := infoMap.(flvio.AMFMap)
	if !ok {
		return nil
	}
	tracks := map[uint8]bool{}
	for key, value := range trackInfos {
		id, err := strconv.Atoi(key)
		if err != nil || id < 1 || id > 255 {
			continue
		}
		codecID := defaultCodecID
		if info, ok := value.(flvio.AMFMap); ok {
			if c, ok := info[codecIDKey]; ok {
				codecID = c
			}
		}
		if isSupported(codecID) {
			tracks[uint8(id)] = true
		}
	}
	return tracks
}

var supportedVideoFourCCs = [][4]byte{
	{'a', 'v', 'c', '1'},
	flvio.FOURCC_HEVC,
	flvio.FOURCC_VP9,
	flvio.FOURCC_AV1,
	flvio.FOURCC_VVC,
}

// isSupportedVideoCodecID returns whether the videocodecid of the metadata is a supported codec. It is a
// legacy codec id or a FourCC as a number (E-RTMP), or a FourCC as a string.
func isSupportedVideoCodecID(id interface{}) bool {
	switch id := id.(type) {
	case float64:
		if id == flvio.VIDEO_H264 {
			return true
		}
		for _, fourcc := range supportedVideoFourCCs {
			if id == flvio.FourCCToFloat(fourcc) {
				return true
			}
		}
	case string:
		for _, fourcc := range supportedVideoFourCCs {
			if id == string(fourcc[:]) {
				return true
			}
		}
	}
	return false
}

var supportedAudioFourCCs = [][4]byte{
	flvio.FOURCC_AAC,
	flvio.FOURCC_OPUS,
	flvio.FOURCC_FLAC,
	flvio.FOURCC_AC3,
	flvio.FOURCC_EAC3,
	flvio.FOURCC_MP3,
}

// isSupportedAudioCodecID returns whether the audiocodecid of the metadata is a supported codec. It is a
// legacy codec id, or the FourCC of AAC, Opus, FLAC, AC-3, E-AC-3 or MP3 as a number or a string (E-RTMP).
func isSupportedAudioCodecID(id interface{}) bool {
	switch id := id.(type) {
	case float64:
		switch id {
		case flvio.SOUND_AAC, flvio.SOUND_SPEEX, flvio.SOUND_NELLYMOSER, flvio.SOUND_NELLYMOSER_16KHZ_MONO, flvio.SOUND_NELLYMOSER_8KHZ_MONO, flvio.SOUND_MP3, flvio.SOUND_MP3_8KHZ:
			return true
		}
		for _, fourCC := range supportedAudioFourCCs {
			if id == flvio.FourCCToFloat(fourCC) {
				return true
			}
		}
	case string:
		for _, fourCC := range supportedAudioFourCCs {
			if id == string(fourCC[:]) {
				return true
			}
		}
	}
	return false
}

// soundRates are the sample rates of the SoundRate of an audio tag.
var soundRates = []int{5512, 11025, 22050, 44100}

// nellymoserFormat returns the sample rate and channel layout of a Nellymoser audio tag.
func nellymoserFormat(tag flvio.Tag) (sampleRate int, channelLayout av.ChannelLayout) {
	switch tag.SoundFormat {
	case flvio.SOUND_NELLYMOSER_16KHZ_MONO:
		return 16000, av.CH_MONO
	case flvio.SOUND_NELLYMOSER_8KHZ_MONO:
		return 8000, av.CH_MONO
	}
	return soundRates[tag.SoundRate&0x3], tag.ChannelLayout()
}

// nellymoserTag returns an audio tag with the SoundFormat and SoundRate for a Nellymoser stream, as FFmpeg
// writes them: 16 kHz and 8 kHz have their own SoundFormat, the other sample rates are in the SoundRate.
func nellymoserTag(stream av.AudioCodecData) flvio.Tag {
	tag := flvio.Tag{
		Type:      flvio.TAG_AUDIO,
		SoundSize: flvio.SOUND_16BIT,
	}

	switch stream.SampleRate() {
	case 16000:
		tag.SoundFormat = flvio.SOUND_NELLYMOSER_16KHZ_MONO
		return tag
	case 8000:
		tag.SoundFormat = flvio.SOUND_NELLYMOSER_8KHZ_MONO
		return tag
	}

	tag.SoundFormat = flvio.SOUND_NELLYMOSER
	for i, rate := range soundRates {
		if stream.SampleRate() == rate {
			tag.SoundRate = uint8(i)
		}
	}
	if stream.ChannelLayout().Count() == 2 {
		tag.SoundType = flvio.SOUND_STEREO
	}
	return tag
}

// mp3Tag returns a legacy audio tag with the SoundFormat, SoundRate and SoundType for an MP3 stream. 8 kHz
// has its own SoundFormat. A sample rate that the SoundRate can't describe (e.g. 48 kHz) is written as
// 44 kHz, as FFmpeg does, the actual one is in the frame header.
func mp3Tag(stream av.AudioCodecData) flvio.Tag {
	tag := flvio.Tag{
		Type:        flvio.TAG_AUDIO,
		SoundFormat: flvio.SOUND_MP3,
		SoundRate:   flvio.SOUND_44Khz,
		SoundSize:   flvio.SOUND_16BIT,
	}

	if stream.SampleRate() == 8000 {
		tag.SoundFormat = flvio.SOUND_MP3_8KHZ
		tag.SoundRate = 0
	} else {
		for i, rate := range soundRates {
			if stream.SampleRate() == rate {
				tag.SoundRate = uint8(i)
			}
		}
	}
	if stream.ChannelLayout().Count() == 2 {
		tag.SoundType = flvio.SOUND_STEREO
	}
	return tag
}

func (prober *Prober) Probed() (ok bool) {
	if prober.MaxProbePacketCount <= 0 {
		prober.MaxProbePacketCount = MaxProbePacketCount
	}

	if prober.HasAudio || prober.HasVideo || len(prober.announcedVideoTracks) != 0 || len(prober.announcedAudioTracks) != 0 || len(prober.pendingAudioTracks) != 0 {
		complete := prober.HasVideo == (prober.videoTracks[0] != nil) && prober.HasAudio == (prober.audioTracks[0] != nil)
		for id := range prober.announcedVideoTracks {
			complete = complete && prober.videoTracks[id] != nil
		}
		for id := range prober.announcedAudioTracks {
			complete = complete && prober.audioTracks[id] != nil
		}
		for id := range prober.pendingAudioTracks {
			complete = complete && prober.audioTracks[id] != nil
		}
		if complete && (prober.hasTrackInfo || prober.gotCodedFrame) {
			prober.sortTracks()
			return true
		}
	}

	if prober.PushedCount == prober.MaxProbePacketCount {
		prober.sortTracks()
		return true
	}

	return false
}

// sortTracks sorts the streams of the tracks of each media type by their track id once probing is done,
// such that the default track (0) is the first stream of its media type. The positions of the audio and
// video streams don't change.
func (prober *Prober) sortTracks() {
	if prober.sorted {
		return
	}
	prober.sorted = true

	streams := append([]av.CodecData(nil), prober.Streams...)
	for _, tracks := range []map[uint8]*probedTrack{prober.videoTracks, prober.audioTracks} {
		ids := []int{}
		slots := []int{}
		for id, track := range tracks {
			ids = append(ids, int(id))
			slots = append(slots, track.idx)
		}
		sort.Ints(ids)
		sort.Ints(slots)
		for i, id := range ids {
			track := tracks[uint8(id)]
			streams[slots[i]] = prober.Streams[track.idx]
			track.idx = slots[i]
		}
	}
	prober.Streams = streams

	if track := prober.videoTracks[lowestTrackID(prober.videoTracks)]; track != nil {
		prober.VideoStreamIdx = track.idx
	}
	if track := prober.audioTracks[lowestTrackID(prober.audioTracks)]; track != nil {
		prober.AudioStreamIdx = track.idx
	}
}

func lowestTrackID(tracks map[uint8]*probedTrack) uint8 {
	lowest := 255
	for id := range tracks {
		if int(id) < lowest {
			lowest = int(id)
		}
	}
	return uint8(lowest)
}

// tagCodecType returns the codec type of an audio or video tag.
func tagCodecType(tag flvio.Tag) av.CodecType {
	switch tag.Type {
	case flvio.TAG_VIDEO:
		if !tag.IsExHeader {
			if tag.CodecID == flvio.VIDEO_H264 {
				return av.H264
			}
			return 0
		}
		switch tag.FourCC {
		case flvio.FOURCC_AVC:
			return av.H264
		case flvio.FOURCC_HEVC:
			return av.HEVC
		case flvio.FOURCC_VP9:
			return av.VP9
		case flvio.FOURCC_AV1:
			return av.AV1
		case flvio.FOURCC_VVC:
			return av.VVC
		}

	case flvio.TAG_AUDIO:
		if tag.IsExHeader {
			switch tag.FourCC {
			case flvio.FOURCC_AAC:
				return av.AAC
			case flvio.FOURCC_OPUS:
				return av.OPUS
			case flvio.FOURCC_FLAC:
				return av.FLAC
			case flvio.FOURCC_AC3:
				return av.AC3
			case flvio.FOURCC_EAC3:
				return av.EAC3
			case flvio.FOURCC_MP3:
				return av.MP3
			}
			return 0
		}
		switch tag.SoundFormat {
		case flvio.SOUND_AAC:
			return av.AAC
		case flvio.SOUND_SPEEX:
			return av.SPEEX
		case flvio.SOUND_MP3, flvio.SOUND_MP3_8KHZ:
			return av.MP3
		case flvio.SOUND_NELLYMOSER, flvio.SOUND_NELLYMOSER_16KHZ_MONO, flvio.SOUND_NELLYMOSER_8KHZ_MONO:
			return av.NELLYMOSER
		}
	}

	return 0
}

// isVideoSequenceStart returns whether the tag is a video sequence header (config record).
func isVideoSequenceStart(tag flvio.Tag) bool {
	if tag.Type != flvio.TAG_VIDEO {
		return false
	}
	if tag.IsExHeader {
		return tag.PacketType == flvio.PKTTYPE_SEQUENCE_START
	}
	return tag.CodecID == flvio.VIDEO_H264 && tag.AVCPacketType == flvio.AVC_SEQHDR
}

// lengthPrefixed returns the NALUs, each prefixed with its length in lengthSize bytes, as in the frames.
func lengthPrefixed(lengthSize int, nalus ...[][]byte) []byte {
	var b []byte
	for _, list := range nalus {
		for _, nalu := range list {
			for i := lengthSize - 1; i >= 0; i-- {
				b = append(b, byte(len(nalu)>>(8*i)))
			}
			b = append(b, nalu...)
		}
	}
	return b
}

// updateVideoConfig handles a sequence header of the video stream after the first one. A re-sent,
// identical config is ignored. A new H.264 or HEVC config is put in-band: its parameter sets are put in
// front of every following key frame, such that a decoder (also one that joins later) uses them instead of
// the ones in the codec data of the stream. A change back to the config of the stream stops that.
//
// Not supported: a new config of other codecs, a change of the codec, and a different NALU length size.
// Streams() keeps the codec data of the first config (e.g. its width and height).
func (prober *Prober) updateVideoConfig(track *probedTrack, tag flvio.Tag) {
	stream := prober.Streams[track.idx]
	if stream.Type() != tagCodecType(tag) || bytes.Equal(tag.Data, track.videoRecord) {
		return
	}

	record := append([]byte(nil), tag.Data...)

	switch stream := stream.(type) {
	case h264parser.CodecData:
		codecData, err := h264parser.NewCodecDataFromAVCDecoderConfRecord(record)
		if err != nil || codecData.RecordInfo.LengthSizeMinusOne != stream.RecordInfo.LengthSizeMinusOne {
			return
		}
		track.videoCodecData = codecData
		track.videoParamSets = lengthPrefixed(int(stream.RecordInfo.LengthSizeMinusOne)+1, codecData.RecordInfo.SPS, codecData.RecordInfo.PPS)
		if bytes.Equal(record, stream.AVCDecoderConfRecordBytes()) {
			track.videoParamSets = nil
		}

	case hevcparser.CodecData:
		codecData, err := hevcparser.NewCodecDataFromHEVCDecoderConfRecord(record)
		if err != nil || codecData.RecordInfo.LengthSizeMinusOne != stream.RecordInfo.LengthSizeMinusOne {
			return
		}
		track.videoCodecData = codecData
		track.videoParamSets = lengthPrefixed(int(stream.RecordInfo.LengthSizeMinusOne)+1, codecData.RecordInfo.VPS, codecData.RecordInfo.SPS, codecData.RecordInfo.PPS)
		if bytes.Equal(record, stream.HEVCDecoderConfRecordBytes()) {
			track.videoParamSets = nil
		}

	default:
		return
	}

	track.videoRecord = record
}

// TagToPacket converts an audio or video tag to a packet of the probed stream of its kind. If there's no
// such stream, or the codec of the tag doesn't match the codec of the stream, it returns false. A tag with
// color info (E-RTMP) updates ColorInfo and returns false. A video sequence header returns false, a new
// config is put in-band into the following key frames (see updateVideoConfig).
func (prober *Prober) TagToPacket(tag flvio.Tag, timestamp int64) (pkt av.Packet, ok bool) {
	if isVideoMetadata(tag) {
		if !prober.isFirstVideoTrack(tag) {
			return
		}
		if colorInfo, found := flvio.ParseColorInfo(tag.Data); found {
			prober.ColorInfo = colorInfo
		}
		return
	}

	track := prober.track(tag)
	if track == nil {
		return
	}

	if isVideoSequenceStart(tag) {
		prober.updateVideoConfig(track, tag)
		return
	}

	if prober.Streams[track.idx].Type() != tagCodecType(tag) {
		return
	}

	pkt.Idx = int8(track.idx)

	switch tag.Type {
	case flvio.TAG_VIDEO:
		// other packet types (e.g. the end of the sequence) are not passed on
		switch tag.PacketType {
		case flvio.PKTTYPE_CODED_FRAMES, flvio.PKTTYPE_CODED_FRAMESX:
			ok = true
			pkt.Data = tag.Data
			pkt.CompositionTime = flvio.TsToTime(tag.CompositionTime)
			pkt.IsKeyFrame = tag.FrameType == flvio.FRAME_KEY
			if pkt.IsKeyFrame && track.videoParamSets != nil {
				data := make([]byte, 0, len(track.videoParamSets)+len(tag.Data))
				data = append(data, track.videoParamSets...)
				pkt.Data = append(data, tag.Data...)
			}
		}

	case flvio.TAG_AUDIO:
		if tag.IsExHeader {
			// other packet types (e.g. a multichannel config) are not passed on, nor are empty coded frames
			// (FFmpeg writes one at the end of a FLAC stream)
			if tag.PacketType == flvio.PKTTYPE_CODED_FRAMES && len(tag.Data) != 0 {
				ok = true
				pkt.Data = tag.Data
				pkt.IsKeyFrame = true
			}
			break
		}
		switch tag.SoundFormat {
		case flvio.SOUND_AAC:
			switch tag.AACPacketType {
			case flvio.AAC_RAW:
				ok = true
				pkt.Data = tag.Data
				pkt.IsKeyFrame = true
			}

		case flvio.SOUND_SPEEX:
			ok = true
			pkt.Data = tag.Data
			pkt.IsKeyFrame = true

		case flvio.SOUND_MP3, flvio.SOUND_MP3_8KHZ:
			if len(tag.Data) != 0 {
				ok = true
				pkt.Data = tag.Data
				pkt.IsKeyFrame = true
			}

		case flvio.SOUND_NELLYMOSER, flvio.SOUND_NELLYMOSER_16KHZ_MONO, flvio.SOUND_NELLYMOSER_8KHZ_MONO:
			ok = true
			pkt.Data = tag.Data
			pkt.IsKeyFrame = true
		}
	}

	pkt.Time = flvio.TsToTime(timestamp)
	return
}

// convertCachedTags converts the tags cached while probing to packets, once probing is done.
func (prober *Prober) convertCachedTags() {
	if len(prober.cachedTags) == 0 || !prober.Probed() {
		return
	}

	// the color info is applied when the packet after it is popped
	update := prober.pendingColorInfo
	for _, c := range prober.cachedTags {
		if isVideoMetadata(c.tag) {
			if !prober.isFirstVideoTrack(c.tag) {
				continue
			}
			if colorInfo, found := flvio.ParseColorInfo(c.tag.Data); found {
				update = &colorInfoUpdate{colorInfo}
			}
			continue
		}
		if pkt, ok := prober.TagToPacket(c.tag, c.timestamp); ok {
			prober.CachedPkts = append(prober.CachedPkts, pkt)
			prober.cachedColorInfo = append(prober.cachedColorInfo, update)
			update = nil
		}
	}
	prober.pendingColorInfo = update

	prober.cachedTags = nil
}

func (prober *Prober) Empty() bool {
	prober.convertCachedTags()
	if len(prober.CachedPkts) != 0 {
		return false
	}

	// the color info after the last cached packet applies to the next packet
	if prober.pendingColorInfo != nil {
		prober.ColorInfo = prober.pendingColorInfo.colorInfo
		prober.pendingColorInfo = nil
	}

	return true
}

func (prober *Prober) PopPacket() av.Packet {
	prober.convertCachedTags()
	pkt := prober.CachedPkts[0]
	prober.CachedPkts = prober.CachedPkts[1:]
	if len(prober.cachedColorInfo) != 0 {
		if update := prober.cachedColorInfo[0]; update != nil {
			prober.ColorInfo = update.colorInfo
		}
		prober.cachedColorInfo = prober.cachedColorInfo[1:]
	}
	return pkt
}

func CodecDataToTag(stream av.CodecData) (_tag flvio.Tag, ok bool, err error) {
	switch stream.Type() {
	case av.H264:
		h264 := stream.(h264parser.CodecData)
		tag := flvio.Tag{
			Type:          flvio.TAG_VIDEO,
			AVCPacketType: flvio.AVC_SEQHDR,
			CodecID:       flvio.VIDEO_H264,
			Data:          h264.AVCDecoderConfRecordBytes(),
			FrameType:     flvio.FRAME_KEY,
		}
		//fmt.Printf("set H264 sequence start:\n%v\n", hex.Dump(tag.Data))
		ok = true
		_tag = tag

	case av.HEVC:
		hevc := stream.(hevcparser.CodecData)
		tag := flvio.Tag{
			Type:       flvio.TAG_VIDEO,
			IsExHeader: true,
			PacketType: flvio.PKTTYPE_SEQUENCE_START,
			FourCC:     flvio.FOURCC_HEVC,
			Data:       hevc.HEVCDecoderConfRecordBytes(),
			FrameType:  flvio.FRAME_KEY,
		}
		//fmt.Printf("set HEVC sequence start:\n%v\n", hex.Dump(tag.Data))
		ok = true
		_tag = tag

	case av.VP9:
		vp9 := stream.(vp9parser.CodecData)
		tag := flvio.Tag{
			Type:       flvio.TAG_VIDEO,
			IsExHeader: true,
			PacketType: flvio.PKTTYPE_SEQUENCE_START,
			FourCC:     flvio.FOURCC_VP9,
			Data:       vp9.VPDecoderConfRecordBytes(),
			FrameType:  flvio.FRAME_KEY,
		}
		//fmt.Printf("set VP9 sequence start:\n%v\n", hex.Dump(tag.Data))
		ok = true
		_tag = tag

	case av.AV1:
		av1 := stream.(av1parser.CodecData)
		tag := flvio.Tag{
			Type:       flvio.TAG_VIDEO,
			IsExHeader: true,
			PacketType: flvio.PKTTYPE_SEQUENCE_START,
			FourCC:     flvio.FOURCC_AV1,
			Data:       av1.AV1DecoderConfRecordBytes(),
			FrameType:  flvio.FRAME_KEY,
		}

		if av1.IsMpeg2TS {
			tag.PacketType = flvio.PKTTYPE_MPEG2TS_SEQUENCE_START
			tag.Data = av1.AV1VideoDescriptorBytes()
		}

		//fmt.Printf("set AV1 sequence start:\n%v\n", hex.Dump(tag.Data))
		ok = true
		_tag = tag

	case av.VVC:
		vvc := stream.(vvcparser.CodecData)
		tag := flvio.Tag{
			Type:       flvio.TAG_VIDEO,
			IsExHeader: true,
			PacketType: flvio.PKTTYPE_SEQUENCE_START,
			FourCC:     flvio.FOURCC_VVC,
			Data:       vvc.VVCDecoderConfRecordBytes(),
			FrameType:  flvio.FRAME_KEY,
		}

		//fmt.Printf("set AV1 sequence start:\n%v\n", hex.Dump(tag.Data))
		ok = true
		_tag = tag

	case av.NELLYMOSER:
	case av.SPEEX:
	case av.MP3:
		// no sequence start, see TrackCodecDataTags

	case av.OPUS:
		// the ID header, or a minimal one for a stream without one
		_tag = flvio.Tag{
			Type:        flvio.TAG_AUDIO,
			SoundFormat: flvio.SOUND_EXHEADER,
			IsExHeader:  true,
			PacketType:  flvio.PKTTYPE_SEQUENCE_START,
			FourCC:      flvio.FOURCC_OPUS,
			Data:        stream.(opusparser.CodecData).OpusHeadBytes(),
		}
		ok = true

	case av.FLAC:
		// the sequence header as it was received, or the "fLaC" marker and the STREAMINFO metadata block
		// (E-RTMP v2)
		_tag = flvio.Tag{
			Type:        flvio.TAG_AUDIO,
			SoundFormat: flvio.SOUND_EXHEADER,
			IsExHeader:  true,
			PacketType:  flvio.PKTTYPE_SEQUENCE_START,
			FourCC:      flvio.FOURCC_FLAC,
			Data:        stream.(flacparser.CodecData).SequenceHeaderBytes(),
		}
		ok = true

	case av.AC3, av.EAC3:
		// an empty sequence start, as FFmpeg writes it, the config is in each syncframe
		fourCC, _ := audioFourCC(stream.Type())
		_tag = flvio.Tag{
			Type:        flvio.TAG_AUDIO,
			SoundFormat: flvio.SOUND_EXHEADER,
			IsExHeader:  true,
			PacketType:  flvio.PKTTYPE_SEQUENCE_START,
			FourCC:      fourCC,
		}
		ok = true

	case av.AAC:
		aac := stream.(aacparser.CodecData)
		// the FLV spec requires 44 kHz and stereo for AAC, the actual sample rate and
		// channels are in the AudioSpecificConfig
		tag := flvio.Tag{
			Type:          flvio.TAG_AUDIO,
			SoundFormat:   flvio.SOUND_AAC,
			SoundRate:     flvio.SOUND_44Khz,
			SoundSize:     flvio.SOUND_16BIT,
			SoundType:     flvio.SOUND_STEREO,
			AACPacketType: flvio.AAC_SEQHDR,
			Data:          aac.MPEG4AudioConfigBytes(),
		}
		ok = true
		_tag = tag

	default:
		err = fmt.Errorf("flv: unspported codecType=%v", stream.Type())
		return
	}
	return
}

// ColorInfoToTag returns a video tag with the color info (HDR metadata, E-RTMP) for the stream. A nil color
// info resets it. ok is false if the codec of the stream has no FourCC, i.e. the color info is only
// defined for HEVC, AV1, VP9 and VVC.
func ColorInfoToTag(colorInfo flvio.AMFMap, stream av.CodecData) (tag flvio.Tag, ok bool) {
	var fourCC [4]byte
	switch stream.Type() {
	case av.HEVC:
		fourCC = flvio.FOURCC_HEVC
	case av.AV1:
		fourCC = flvio.FOURCC_AV1
	case av.VP9:
		fourCC = flvio.FOURCC_VP9
	case av.VVC:
		fourCC = flvio.FOURCC_VVC
	default:
		return
	}

	tag = flvio.Tag{
		Type:       flvio.TAG_VIDEO,
		IsExHeader: true,
		PacketType: flvio.PKTTYPE_METADATA,
		// the frame type is to be ignored, FFmpeg writes a command frame
		FrameType: flvio.FRAME_COMMAND,
		FourCC:    fourCC,
		Data:      flvio.ColorInfoData(colorInfo),
	}
	ok = true
	return
}

func PacketToTag(pkt av.Packet, stream av.CodecData) (tag flvio.Tag, timestamp int64) {
	switch stream.Type() {
	case av.H264:
		tag = flvio.Tag{
			Type:            flvio.TAG_VIDEO,
			AVCPacketType:   flvio.AVC_NALU,
			CodecID:         flvio.VIDEO_H264,
			Data:            pkt.Data,
			CompositionTime: flvio.TimeToTs(pkt.CompositionTime),
		}
		if pkt.IsKeyFrame {
			tag.FrameType = flvio.FRAME_KEY
		} else {
			tag.FrameType = flvio.FRAME_INTER
		}

	case av.HEVC:
		tag = flvio.Tag{
			Type:            flvio.TAG_VIDEO,
			IsExHeader:      true,
			PacketType:      flvio.PKTTYPE_CODED_FRAMES,
			CompositionTime: flvio.TimeToTs(pkt.CompositionTime),
			FourCC:          flvio.FOURCC_HEVC,
			Data:            pkt.Data,
		}

		if pkt.CompositionTime == 0 {
			tag.PacketType = flvio.PKTTYPE_CODED_FRAMESX
		}

		if pkt.IsKeyFrame {
			tag.FrameType = flvio.FRAME_KEY
		} else {
			tag.FrameType = flvio.FRAME_INTER
		}

	case av.VP9:
		tag = flvio.Tag{
			Type:            flvio.TAG_VIDEO,
			IsExHeader:      true,
			PacketType:      flvio.PKTTYPE_CODED_FRAMES,
			CompositionTime: flvio.TimeToTs(pkt.CompositionTime),
			FourCC:          flvio.FOURCC_VP9,
			Data:            pkt.Data,
		}

		if pkt.IsKeyFrame {
			tag.FrameType = flvio.FRAME_KEY
		} else {
			tag.FrameType = flvio.FRAME_INTER
		}

	case av.AV1:
		tag = flvio.Tag{
			Type:            flvio.TAG_VIDEO,
			IsExHeader:      true,
			PacketType:      flvio.PKTTYPE_CODED_FRAMES,
			CompositionTime: flvio.TimeToTs(pkt.CompositionTime),
			FourCC:          flvio.FOURCC_AV1,
			Data:            pkt.Data,
		}

		if pkt.IsKeyFrame {
			tag.FrameType = flvio.FRAME_KEY
		} else {
			tag.FrameType = flvio.FRAME_INTER
		}

	case av.VVC:
		tag = flvio.Tag{
			Type:            flvio.TAG_VIDEO,
			IsExHeader:      true,
			PacketType:      flvio.PKTTYPE_CODED_FRAMES,
			CompositionTime: flvio.TimeToTs(pkt.CompositionTime),
			FourCC:          flvio.FOURCC_VVC,
			Data:            pkt.Data,
		}

		if pkt.CompositionTime == 0 {
			tag.PacketType = flvio.PKTTYPE_CODED_FRAMESX
		}

		if pkt.IsKeyFrame {
			tag.FrameType = flvio.FRAME_KEY
		} else {
			tag.FrameType = flvio.FRAME_INTER
		}

	case av.AAC:
		// see CodecDataToTag
		tag = flvio.Tag{
			Type:          flvio.TAG_AUDIO,
			SoundFormat:   flvio.SOUND_AAC,
			SoundRate:     flvio.SOUND_44Khz,
			SoundSize:     flvio.SOUND_16BIT,
			SoundType:     flvio.SOUND_STEREO,
			AACPacketType: flvio.AAC_RAW,
			Data:          pkt.Data,
		}

	case av.OPUS, av.FLAC, av.AC3, av.EAC3:
		fourCC, _ := audioFourCC(stream.Type())
		tag = flvio.Tag{
			Type:        flvio.TAG_AUDIO,
			SoundFormat: flvio.SOUND_EXHEADER,
			IsExHeader:  true,
			PacketType:  flvio.PKTTYPE_CODED_FRAMES,
			FourCC:      fourCC,
			Data:        pkt.Data,
		}

	case av.SPEEX:
		tag = flvio.Tag{
			Type:        flvio.TAG_AUDIO,
			SoundFormat: flvio.SOUND_SPEEX,
			Data:        pkt.Data,
		}

	case av.NELLYMOSER:
		tag = nellymoserTag(stream.(av.AudioCodecData))
		tag.Data = pkt.Data

	case av.MP3:
		tag = mp3Tag(stream.(av.AudioCodecData))
		tag.Data = pkt.Data
	}

	timestamp = flvio.TimeToTs(pkt.Time)
	return
}

type Muxer struct {
	bufw     writeFlusher
	b        []byte
	streams  []av.CodecData
	trackIDs []uint8

	colorInfo        flvio.AMFMap
	colorInfoPending bool

	// a seekable writer (see NewMuxer), the duration and file size in the metadata are updated in WriteTrailer
	seeker io.WriteSeeker
	// the position of the file in the writer, and the positions of the duration and file size values
	start, durationPos, filesizePos int64
	// the time of the last packet
	lastTime int64
}

type writeFlusher interface {
	io.Writer
	Flush() error
}

func NewMuxerWriteFlusher(w writeFlusher) *Muxer {
	return &Muxer{
		bufw: w,
		b:    make([]byte, 256),
	}
}

// NewMuxer returns a muxer that writes to w. If w is seekable (e.g. a file), the metadata has the duration and
// the file size, which are written in WriteTrailer.
func NewMuxer(w io.Writer) *Muxer {
	muxer := NewMuxerWriteFlusher(bufio.NewWriterSize(w, pio.RecommendBufioSize))
	if seeker, ok := w.(io.WriteSeeker); ok {
		muxer.seeker = seeker
	}
	return muxer
}

// CodecTypes are the codecs that the muxer writes.
var CodecTypes = []av.CodecType{av.H264, av.HEVC, av.VP9, av.AV1, av.VVC, av.AAC, av.SPEEX, av.NELLYMOSER, av.OPUS, av.FLAC, av.AC3, av.EAC3, av.MP3}

func (muxer *Muxer) WriteHeader(streams []av.CodecData) (err error) {
	var flags uint8
	for _, stream := range streams {
		if stream.Type().IsVideo() {
			flags |= flvio.FILE_HAS_VIDEO
		} else if stream.Type().IsAudio() {
			flags |= flvio.FILE_HAS_AUDIO
		}
	}

	var trackIDs []uint8
	if trackIDs, err = TrackIDs(streams); err != nil {
		return
	}

	var metadata flvio.AMFMap
	if metadata, err = NewMetadataByStreams(streams); err != nil {
		return
	}

	if muxer.seeker != nil {
		// e.g. a pipe isn't seekable, although it is an *os.File
		if muxer.start, err = muxer.seeker.Seek(0, io.SeekCurrent); err != nil {
			muxer.seeker = nil
			err = nil
		}
	}
	if muxer.seeker != nil {
		// placeholders, see WriteTrailer
		metadata["duration"] = 0.0
		metadata["filesize"] = 0.0
	}

	n := flvio.FillFileHeader(muxer.b, flags)
	if _, err = muxer.bufw.Write(muxer.b[:n]); err != nil {
		return
	}

	metadataTag := MetadataToTag(metadata)
	if err = flvio.WriteTag(muxer.bufw, metadataTag, 0, muxer.b); err != nil {
		return
	}
	if muxer.seeker != nil {
		pos := muxer.start + int64(n+flvio.TagHeaderLength)
		duration, filesize := amfNumberOffset(metadataTag.Data, "duration"), amfNumberOffset(metadataTag.Data, "filesize")
		if duration < 0 || filesize < 0 {
			muxer.seeker = nil
		}
		muxer.durationPos = pos + int64(duration)
		muxer.filesizePos = pos + int64(filesize)
	}

	for i, stream := range streams {
		var tags []flvio.Tag
		if tags, err = TrackCodecDataTags(stream, trackIDs[i]); err != nil {
			return
		}
		for _, tag := range tags {
			if err = flvio.WriteTag(muxer.bufw, tag, 0, muxer.b); err != nil {
				return
			}
		}
	}

	muxer.streams = streams
	muxer.trackIDs = trackIDs
	return
}

// SetColorInfo sets the color info (HDR metadata, E-RTMP). It is written before the next video packet if
// the video codec is HEVC, AV1, VP9 or VVC, otherwise it is dropped. A nil color info resets it.
func (muxer *Muxer) SetColorInfo(colorInfo flvio.AMFMap) {
	muxer.colorInfo = colorInfo
	muxer.colorInfoPending = true
}

func (muxer *Muxer) WritePacket(pkt av.Packet) (err error) {
	stream := muxer.streams[pkt.Idx]
	trackID := muxer.trackIDs[pkt.Idx]
	tag, timestamp := PacketToTag(pkt, stream)
	tag = TrackTag(tag, trackID)

	// the color info is about the default video track
	if muxer.colorInfoPending && stream.Type().IsVideo() && trackID == 0 {
		muxer.colorInfoPending = false
		if colorInfoTag, ok := ColorInfoToTag(muxer.colorInfo, stream); ok {
			if err = flvio.WriteTag(muxer.bufw, colorInfoTag, timestamp, muxer.b); err != nil {
				return
			}
		}
	}

	if err = flvio.WriteTag(muxer.bufw, tag, timestamp, muxer.b); err != nil {
		return
	}
	if pkt.Time > muxer.lastTime {
		muxer.lastTime = pkt.Time
	}
	return
}

// WriteTrailer flushes the written tags. With a seekable writer, it writes the duration (the time of the last
// packet) and the file size into the metadata.
func (muxer *Muxer) WriteTrailer() (err error) {
	if err = muxer.bufw.Flush(); err != nil {
		return
	}
	if muxer.seeker == nil {
		return
	}

	var end int64
	if end, err = muxer.seeker.Seek(0, io.SeekCurrent); err != nil {
		return
	}
	for _, v := range []struct {
		pos   int64
		value float64
	}{
		{muxer.durationPos, float64(muxer.lastTime) / 1000},
		{muxer.filesizePos, float64(end - muxer.start)},
	} {
		if _, err = muxer.seeker.Seek(v.pos, io.SeekStart); err != nil {
			return
		}
		binary.BigEndian.PutUint64(muxer.b, math.Float64bits(v.value))
		if _, err = muxer.seeker.Write(muxer.b[:8]); err != nil {
			return
		}
	}
	_, err = muxer.seeker.Seek(end, io.SeekStart)
	return
}

// MetadataToTag returns the script tag of the metadata (onMetaData).
func MetadataToTag(metadata flvio.AMFMap) flvio.Tag {
	name := "onMetaData"
	data := make([]byte, flvio.LenAMF0Val(name)+flvio.LenAMF0Val(metadata))
	n := flvio.FillAMF0Val(data, name)
	flvio.FillAMF0Val(data[n:], metadata)
	return flvio.Tag{Type: flvio.TAG_SCRIPTDATA, Data: data}
}

// amfNumberOffset returns the offset of the value of the number property key of the encoded AMF0 object in
// data, or -1.
func amfNumberOffset(data []byte, key string) int {
	property := append([]byte{0, byte(len(key))}, key...)
	property = append(property, 0x00) // number marker
	i := bytes.Index(data, property)
	if i < 0 {
		return -1
	}
	return i + len(property)
}

type Demuxer struct {
	prober *Prober
	bufr   *bufio.Reader
	b      []byte
	stage  int

	// the tags of the last multitrack tag (E-RTMP v2) that haven't been returned by readTag yet
	tags      []flvio.Tag
	timestamp int64
}

// readTag returns the next tag, or the next track of a multitrack tag with several tracks.
func (demuxer *Demuxer) readTag() (tag flvio.Tag, timestamp int64, err error) {
	for len(demuxer.tags) == 0 {
		if tag, demuxer.timestamp, err = flvio.ReadTag(demuxer.bufr, demuxer.b); err != nil {
			return
		}
		if demuxer.tags, err = flvio.SplitTracks(tag); err != nil {
			return
		}
	}
	tag = demuxer.tags[0]
	demuxer.tags = demuxer.tags[1:]
	return tag, demuxer.timestamp, nil
}

func NewDemuxer(r io.Reader) *Demuxer {
	return &Demuxer{
		bufr:   bufio.NewReaderSize(r, pio.RecommendBufioSize),
		prober: &Prober{},
		b:      make([]byte, 256),
	}
}

func (demuxer *Demuxer) prepare() (err error) {
	for demuxer.stage < 2 {
		switch demuxer.stage {
		case 0:
			if _, err = io.ReadFull(demuxer.bufr, demuxer.b[:flvio.FileHeaderLength]); err != nil {
				return
			}
			var flags uint8
			var skip int
			if flags, skip, err = flvio.ParseFileHeader(demuxer.b); err != nil {
				return
			}
			if _, err = demuxer.bufr.Discard(skip); err != nil {
				return
			}
			if flags&flvio.FILE_HAS_AUDIO != 0 {
				demuxer.prober.HasAudio = true
			}
			if flags&flvio.FILE_HAS_VIDEO != 0 {
				demuxer.prober.HasVideo = true
			}
			demuxer.stage++

		case 1:
			for !demuxer.prober.Probed() {
				var tag flvio.Tag
				var timestamp int64
				if tag, timestamp, err = demuxer.readTag(); err != nil {
					return
				}
				if tag.Type == flvio.TAG_SCRIPTDATA {
					demuxer.pushMetadata(tag)
					continue
				}
				if err = demuxer.prober.PushTag(tag, timestamp); err != nil {
					return
				}
			}
			demuxer.stage++
		}
	}
	return
}

func (demuxer *Demuxer) Streams() (streams []av.CodecData, err error) {
	if err = demuxer.prepare(); err != nil {
		return
	}
	streams = demuxer.prober.Streams
	return
}

// GetColorInfo returns the color info (HDR metadata, E-RTMP) that applies to the video packet read last and
// the following ones. It is nil if no color info has been received or it has been reset.
func (demuxer *Demuxer) GetColorInfo() flvio.AMFMap {
	return demuxer.prober.ColorInfo
}

// pushMetadata passes the metadata (onMetaData) of a script tag to the prober. The codecs in the metadata
// replace the flags of the file header, such that an unsupported codec isn't waited for.
func (demuxer *Demuxer) pushMetadata(tag flvio.Tag) {
	metadata := ParseMetadata(tag.Data)
	if metadata == nil {
		return
	}
	if _, ok := metadata["videocodecid"]; ok {
		demuxer.prober.HasVideo = false
	}
	if _, ok := metadata["audiocodecid"]; ok {
		demuxer.prober.HasAudio = false
	}
	demuxer.prober.PushMetadata(metadata)
}

// ParseMetadata returns the metadata (onMetaData) in the data of a script tag, or nil.
func ParseMetadata(data []byte) flvio.AMFMap {
	var vals []interface{}
	for len(data) != 0 {
		val, n, err := flvio.ParseAMF0Val(data)
		if err != nil {
			break
		}
		vals = append(vals, val)
		data = data[n:]
	}
	// "onMetaData" is followed by the metadata, also after "@setDataFrame"
	for i, val := range vals {
		if name, ok := val.(string); ok && name == "onMetaData" && i+1 < len(vals) {
			if metadata, ok := vals[i+1].(flvio.AMFMap); ok {
				return metadata
			}
		}
	}
	return nil
}

func (demuxer *Demuxer) ReadPacket() (pkt av.Packet, err error) {
	if err = demuxer.prepare(); err != nil {
		return
	}

	if !demuxer.prober.Empty() {
		pkt = demuxer.prober.PopPacket()
		return
	}

	for {
		var tag flvio.Tag
		var timestamp int64
		if tag, timestamp, err = demuxer.readTag(); err != nil {
			return
		}

		var ok bool
		if pkt, ok = demuxer.prober.TagToPacket(tag, timestamp); ok {
			return
		}
	}
}

func Handler(h *avutil.RegisterHandler) {
	h.Probe = func(b []byte) bool {
		return b[0] == 'F' && b[1] == 'L' && b[2] == 'V'
	}

	h.Ext = ".flv"

	h.ReaderDemuxer = func(r io.Reader) av.Demuxer {
		return NewDemuxer(r)
	}

	h.WriterMuxer = func(w io.Writer) av.Muxer {
		return NewMuxer(w)
	}

	h.CodecTypes = CodecTypes
}
