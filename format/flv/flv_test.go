package flv

import (
	"bytes"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"testing"
	"time"

	"github.com/datarhei/joy4/av"
	"github.com/datarhei/joy4/codec"
	"github.com/datarhei/joy4/codec/aacparser"
	"github.com/datarhei/joy4/codec/ac3parser"
	"github.com/datarhei/joy4/codec/fake"
	"github.com/datarhei/joy4/codec/flacparser"
	"github.com/datarhei/joy4/codec/h264parser"
	"github.com/datarhei/joy4/codec/hevcparser"
	"github.com/datarhei/joy4/codec/mp3parser"
	"github.com/datarhei/joy4/codec/opusparser"
	"github.com/datarhei/joy4/codec/vvcparser"
	"github.com/datarhei/joy4/format/flv/flvio"
	"github.com/stretchr/testify/require"
)

func TestProberVVC(t *testing.T) {
	record := []byte{0x01, 0x02, 0x03, 0x04}

	prober := NewProber(0)
	prober.HasVideo = true

	err := prober.PushTag(flvio.Tag{
		Type:       flvio.TAG_VIDEO,
		IsExHeader: true,
		FrameType:  flvio.FRAME_KEY,
		PacketType: flvio.PKTTYPE_SEQUENCE_START,
		FourCC:     flvio.FOURCC_VVC,
		Data:       record,
	}, 0)
	require.NoError(t, err)

	pushCodedFrame(t, prober)
	require.True(t, prober.Probed())
	require.Len(t, prober.Streams, 1)
	require.Equal(t, av.VVC, prober.Streams[0].Type())
	require.Equal(t, record, prober.Streams[0].(vvcparser.CodecData).VVCDecoderConfRecordBytes())

	pkt, ok := prober.TagToPacket(flvio.Tag{
		Type:       flvio.TAG_VIDEO,
		IsExHeader: true,
		FrameType:  flvio.FRAME_KEY,
		PacketType: flvio.PKTTYPE_CODED_FRAMESX,
		FourCC:     flvio.FOURCC_VVC,
		Data:       []byte{0xAA},
	}, 40)
	require.True(t, ok)
	require.Equal(t, int8(0), pkt.Idx)
	require.True(t, pkt.IsKeyFrame)
	require.Equal(t, []byte{0xAA}, pkt.Data)
	require.Equal(t, int64(40), pkt.Time)
}

func TestMuxDemuxVVC(t *testing.T) {
	stream, err := vvcparser.NewCodecDataFromVVCDecoderConfRecord([]byte{0x01, 0x02, 0x03, 0x04})
	require.NoError(t, err)

	buf := &bytes.Buffer{}
	muxer := NewMuxer(buf)

	err = muxer.WriteHeader([]av.CodecData{stream})
	require.NoError(t, err)

	for i := 0; i < 3; i++ {
		err = muxer.WritePacket(av.Packet{Idx: 0, IsKeyFrame: i == 0, Data: []byte{byte(i)}, Time: int64(40 * i)})
		require.NoError(t, err)
	}

	err = muxer.WriteTrailer()
	require.NoError(t, err)

	demuxer := NewDemuxer(bytes.NewReader(buf.Bytes()))

	streams, err := demuxer.Streams()
	require.NoError(t, err)
	require.Len(t, streams, 1)
	require.Equal(t, av.VVC, streams[0].Type())
	require.Equal(t, []byte{0x01, 0x02, 0x03, 0x04}, streams[0].(vvcparser.CodecData).VVCDecoderConfRecordBytes())

	for i := 0; i < 3; i++ {
		pkt, err := demuxer.ReadPacket()
		require.NoError(t, err)
		require.Equal(t, []byte{byte(i)}, pkt.Data)
		require.Equal(t, int64(40*i), pkt.Time)
		require.Equal(t, i == 0, pkt.IsKeyFrame)
	}
}

func TestMuxDemuxVVCCompositionTime(t *testing.T) {
	stream, err := vvcparser.NewCodecDataFromVVCDecoderConfRecord([]byte{0x01, 0x02, 0x03, 0x04})
	require.NoError(t, err)

	buf := &bytes.Buffer{}
	muxer := NewMuxer(buf)

	err = muxer.WriteHeader([]av.CodecData{stream})
	require.NoError(t, err)

	cts := []int64{80, 0, 40}
	for i, c := range cts {
		err = muxer.WritePacket(av.Packet{Idx: 0, IsKeyFrame: i == 0, Data: []byte{0xAA, byte(i)}, Time: int64(40 * i), CompositionTime: c})
		require.NoError(t, err)
	}

	err = muxer.WriteTrailer()
	require.NoError(t, err)

	demuxer := NewDemuxer(bytes.NewReader(buf.Bytes()))

	for i, c := range cts {
		pkt, err := demuxer.ReadPacket()
		require.NoError(t, err)
		require.Equal(t, []byte{0xAA, byte(i)}, pkt.Data, "packet %d", i)
		require.Equal(t, c, pkt.CompositionTime, "packet %d", i)
	}
}

// pushCodedFrame pushes a frame of an unsupported codec (ADPCM). Without a TrackIdInfoMap, probing ends at
// the first coded frame after the announced streams.
func pushCodedFrame(t *testing.T, prober *Prober) {
	require.NoError(t, prober.PushTag(flvio.Tag{Type: flvio.TAG_AUDIO, SoundFormat: 1, Data: []byte{0xFF}}, 0))
}

func aacTag(packetType uint8, data []byte) flvio.Tag {
	return flvio.Tag{Type: flvio.TAG_AUDIO, SoundFormat: flvio.SOUND_AAC, SoundRate: flvio.SOUND_44Khz, SoundSize: flvio.SOUND_16BIT, SoundType: flvio.SOUND_STEREO, AACPacketType: packetType, Data: data}
}

func exVideoTag(fourcc [4]byte, packetType uint8, data []byte) flvio.Tag {
	return flvio.Tag{Type: flvio.TAG_VIDEO, IsExHeader: true, FrameType: flvio.FRAME_KEY, PacketType: packetType, FourCC: fourcc, Data: data}
}

func TestProberNoVideoStream(t *testing.T) {
	// audio only
	prober := NewProber(0)
	prober.HasAudio = true

	require.NoError(t, prober.PushTag(aacTag(flvio.AAC_SEQHDR, []byte{0x12, 0x10}), 0))
	pushCodedFrame(t, prober)
	require.True(t, prober.Probed())
	require.Len(t, prober.Streams, 1)

	pkt, ok := prober.TagToPacket(aacTag(flvio.AAC_RAW, []byte{0xAA}), 23)
	require.True(t, ok)
	require.Equal(t, int8(0), pkt.Idx)

	// a video frame must not become a packet of the audio stream
	_, ok = prober.TagToPacket(flvio.Tag{Type: flvio.TAG_VIDEO, FrameType: flvio.FRAME_KEY, CodecID: flvio.VIDEO_H264, AVCPacketType: flvio.AVC_NALU, Data: []byte{0xBB}}, 23)
	require.False(t, ok)
	_, ok = prober.TagToPacket(exVideoTag(flvio.FOURCC_VVC, flvio.PKTTYPE_CODED_FRAMESX, []byte{0xBB}), 23)
	require.False(t, ok)
}

func TestProberCodecMismatch(t *testing.T) {
	prober := NewProber(0)
	prober.HasVideo = true

	require.NoError(t, prober.PushTag(exVideoTag(flvio.FOURCC_VVC, flvio.PKTTYPE_SEQUENCE_START, []byte{0x01}), 0))
	pushCodedFrame(t, prober)
	require.True(t, prober.Probed())

	_, ok := prober.TagToPacket(exVideoTag(flvio.FOURCC_VVC, flvio.PKTTYPE_CODED_FRAMESX, []byte{0xBB}), 40)
	require.True(t, ok)

	// a frame of another codec doesn't belong to the probed video stream
	_, ok = prober.TagToPacket(exVideoTag(flvio.FOURCC_HEVC, flvio.PKTTYPE_CODED_FRAMESX, []byte{0xBB}), 40)
	require.False(t, ok)
}

func TestProberCachedBeforeSequenceHeader(t *testing.T) {
	prober := NewProber(0)
	prober.HasAudio = true
	prober.HasVideo = true

	// audio becomes stream 0, a video frame arrives before the sequence header of the video stream
	require.NoError(t, prober.PushTag(aacTag(flvio.AAC_SEQHDR, []byte{0x12, 0x10}), 0))
	require.NoError(t, prober.PushTag(exVideoTag(flvio.FOURCC_VVC, flvio.PKTTYPE_CODED_FRAMESX, []byte{0xBB}), 0))
	require.NoError(t, prober.PushTag(aacTag(flvio.AAC_RAW, []byte{0xAA}), 23))
	require.False(t, prober.Probed())
	require.NoError(t, prober.PushTag(exVideoTag(flvio.FOURCC_VVC, flvio.PKTTYPE_SEQUENCE_START, []byte{0x01}), 40))
	require.True(t, prober.Probed())

	require.Len(t, prober.Streams, 2)
	require.Equal(t, av.AAC, prober.Streams[0].Type())
	require.Equal(t, av.VVC, prober.Streams[1].Type())

	require.False(t, prober.Empty())
	pkt := prober.PopPacket()
	require.Equal(t, int8(1), pkt.Idx)
	require.Equal(t, []byte{0xBB}, pkt.Data)

	require.False(t, prober.Empty())
	pkt = prober.PopPacket()
	require.Equal(t, int8(0), pkt.Idx)
	require.Equal(t, []byte{0xAA}, pkt.Data)

	require.True(t, prober.Empty())
}

func TestProberCachedWithoutStream(t *testing.T) {
	// the metadata only announces audio, but there are video frames without a sequence header
	prober := NewProber(0)
	prober.HasAudio = true

	require.NoError(t, prober.PushTag(exVideoTag(flvio.FOURCC_VVC, flvio.PKTTYPE_CODED_FRAMESX, []byte{0xBB}), 0))
	require.NoError(t, prober.PushTag(aacTag(flvio.AAC_SEQHDR, []byte{0x12, 0x10}), 0))
	require.True(t, prober.Probed())

	// the cached video frame is dropped
	require.True(t, prober.Empty())
}

func TestProberLegacyCodec(t *testing.T) {
	// the metadata announces audio and video, the video is Sorenson H.263 (codec id 2)
	prober := NewProber(4)
	prober.HasAudio = true
	prober.HasVideo = true

	h263 := func(data []byte) flvio.Tag {
		tag := flvio.Tag{Type: flvio.TAG_VIDEO}
		_, err := tag.ParseHeader(append([]byte{0x12}, data...))
		require.NoError(t, err)
		tag.Data = data
		return tag
	}

	// the first byte of H.263 data would be AVC_SEQHDR, the second AVC_NALU
	require.NoError(t, prober.PushTag(h263([]byte{0x00, 0x00, 0x84, 0x00}), 0))
	require.NoError(t, prober.PushTag(aacTag(flvio.AAC_SEQHDR, []byte{0x12, 0x10}), 0))
	require.NoError(t, prober.PushTag(h263([]byte{0x01, 0x00, 0x84, 0x00}), 40))
	require.NoError(t, prober.PushTag(aacTag(flvio.AAC_RAW, []byte{0xAA}), 23))
	require.True(t, prober.Probed())

	require.Len(t, prober.Streams, 1)
	require.Equal(t, av.AAC, prober.Streams[0].Type())

	_, ok := prober.TagToPacket(h263([]byte{0x01, 0x02}), 80)
	require.False(t, ok)

	// only the audio packet is cached
	require.False(t, prober.Empty())
	pkt := prober.PopPacket()
	require.Equal(t, int8(0), pkt.Idx)
	require.Equal(t, []byte{0xAA}, pkt.Data)
	require.True(t, prober.Empty())
}

func colorInfoTag(colorInfo flvio.AMFMap) flvio.Tag {
	return flvio.Tag{Type: flvio.TAG_VIDEO, IsExHeader: true, FrameType: flvio.FRAME_COMMAND, PacketType: flvio.PKTTYPE_METADATA, FourCC: flvio.FOURCC_VVC, Data: flvio.ColorInfoData(colorInfo)}
}

func TestMuxDemuxColorInfo(t *testing.T) {
	stream, err := vvcparser.NewCodecDataFromVVCDecoderConfRecord([]byte{0x01, 0x02, 0x03, 0x04})
	require.NoError(t, err)

	infoA := flvio.AMFMap{"colorConfig": flvio.AMFMap{"colorPrimaries": 9.0}}
	infoB := flvio.AMFMap{"hdrCll": flvio.AMFMap{"maxFall": 400.0, "maxCLL": 1000.0}}

	buf := &bytes.Buffer{}
	muxer := NewMuxer(buf)

	err = muxer.WriteHeader([]av.CodecData{stream})
	require.NoError(t, err)

	// the color info before the first packet, a new one before the third packet, a reset before the fourth
	expected := []flvio.AMFMap{infoA, infoA, infoB, nil}
	for i := 0; i < 4; i++ {
		switch i {
		case 0:
			muxer.SetColorInfo(infoA)
		case 2:
			muxer.SetColorInfo(infoB)
		case 3:
			muxer.SetColorInfo(nil)
		}
		err = muxer.WritePacket(av.Packet{Idx: 0, IsKeyFrame: i == 0, Data: []byte{byte(i)}, Time: int64(40 * i)})
		require.NoError(t, err)
	}

	err = muxer.WriteTrailer()
	require.NoError(t, err)

	demuxer := NewDemuxer(bytes.NewReader(buf.Bytes()))

	_, err = demuxer.Streams()
	require.NoError(t, err)
	require.Nil(t, demuxer.GetColorInfo())

	for i := 0; i < 4; i++ {
		pkt, err := demuxer.ReadPacket()
		require.NoError(t, err)
		require.Equal(t, []byte{byte(i)}, pkt.Data, "packet %d", i)
		require.Equal(t, expected[i], demuxer.GetColorInfo(), "packet %d", i)
	}
}

func TestProberColorInfoWhileProbing(t *testing.T) {
	infoA := flvio.AMFMap{"a": 1.0}
	infoB := flvio.AMFMap{"b": 2.0}
	infoC := flvio.AMFMap{"c": 3.0}

	prober := NewProber(0)
	prober.HasAudio = true
	prober.HasVideo = true

	// the color info is received while probing, between the cached packets
	require.NoError(t, prober.PushTag(aacTag(flvio.AAC_SEQHDR, []byte{0x12, 0x10}), 0))
	require.NoError(t, prober.PushTag(colorInfoTag(infoA), 0))
	require.NoError(t, prober.PushTag(exVideoTag(flvio.FOURCC_VVC, flvio.PKTTYPE_CODED_FRAMESX, []byte{0xBB}), 0))
	require.NoError(t, prober.PushTag(colorInfoTag(infoB), 23))
	require.NoError(t, prober.PushTag(aacTag(flvio.AAC_RAW, []byte{0xAA}), 23))
	require.NoError(t, prober.PushTag(exVideoTag(flvio.FOURCC_VVC, flvio.PKTTYPE_SEQUENCE_END, nil), 40))
	require.NoError(t, prober.PushTag(colorInfoTag(infoC), 40))
	require.NoError(t, prober.PushTag(exVideoTag(flvio.FOURCC_VVC, flvio.PKTTYPE_SEQUENCE_START, []byte{0x01}), 40))
	require.True(t, prober.Probed())
	require.Nil(t, prober.ColorInfo)

	require.False(t, prober.Empty())
	pkt := prober.PopPacket()
	require.Equal(t, []byte{0xBB}, pkt.Data)
	require.Equal(t, infoA, prober.ColorInfo)

	require.False(t, prober.Empty())
	pkt = prober.PopPacket()
	require.Equal(t, []byte{0xAA}, pkt.Data)
	require.Equal(t, infoB, prober.ColorInfo)

	// the end of the sequence is not a packet, the last color info applies to the next packet
	require.True(t, prober.Empty())
	require.Equal(t, infoC, prober.ColorInfo)

	// after probing
	_, ok := prober.TagToPacket(colorInfoTag(nil), 80)
	require.False(t, ok)
	require.Nil(t, prober.ColorInfo)
	_, ok = prober.TagToPacket(exVideoTag(flvio.FOURCC_VVC, flvio.PKTTYPE_SEQUENCE_END, nil), 80)
	require.False(t, ok)
}

func TestColorInfoToTag(t *testing.T) {
	info := flvio.AMFMap{"a": 1.0}

	for _, tc := range []struct {
		codec  av.CodecType
		fourcc [4]byte
	}{
		{av.HEVC, flvio.FOURCC_HEVC},
		{av.AV1, flvio.FOURCC_AV1},
		{av.VP9, flvio.FOURCC_VP9},
		{av.VVC, flvio.FOURCC_VVC},
	} {
		tag, ok := ColorInfoToTag(info, fake.CodecData{CodecType_: tc.codec})
		require.True(t, ok, "%v", tc.codec)
		require.Equal(t, tc.fourcc, tag.FourCC)

		b := make([]byte, flvio.MaxTagSubHeaderLength)
		n := tag.FillHeader(b)
		require.Equal(t, append([]byte{0xD4}, tc.fourcc[:]...), b[:n])
	}

	// not defined for H.264
	_, ok := ColorInfoToTag(info, fake.CodecData{CodecType_: av.H264})
	require.False(t, ok)
}

func TestNellymoserTags(t *testing.T) {
	tests := []struct {
		sampleRate    int
		channelLayout av.ChannelLayout
		soundFormat   uint8
		soundRate     uint8
		soundType     uint8
	}{
		{8000, av.CH_MONO, flvio.SOUND_NELLYMOSER_8KHZ_MONO, 0, flvio.SOUND_MONO},
		{16000, av.CH_MONO, flvio.SOUND_NELLYMOSER_16KHZ_MONO, 0, flvio.SOUND_MONO},
		{5512, av.CH_MONO, flvio.SOUND_NELLYMOSER, flvio.SOUND_5_5Khz, flvio.SOUND_MONO},
		{11025, av.CH_MONO, flvio.SOUND_NELLYMOSER, flvio.SOUND_11Khz, flvio.SOUND_MONO},
		{22050, av.CH_MONO, flvio.SOUND_NELLYMOSER, flvio.SOUND_22Khz, flvio.SOUND_MONO},
		{44100, av.CH_MONO, flvio.SOUND_NELLYMOSER, flvio.SOUND_44Khz, flvio.SOUND_MONO},
		{44100, av.CH_STEREO, flvio.SOUND_NELLYMOSER, flvio.SOUND_44Khz, flvio.SOUND_STEREO},
	}

	for _, test := range tests {
		stream := codec.NewNellymoserCodecData(test.sampleRate, test.channelLayout)

		metadata, err := NewMetadataByStreams([]av.CodecData{stream})
		require.NoError(t, err)
		require.Equal(t, flvio.AMFMap{"audiocodecid": int(test.soundFormat), "audiosamplerate": test.sampleRate}, metadata)

		tag, _ := PacketToTag(av.Packet{Data: make([]byte, 64)}, stream)
		require.Equal(t, test.soundFormat, tag.SoundFormat, "%d Hz", test.sampleRate)
		require.Equal(t, test.soundRate, tag.SoundRate, "%d Hz", test.sampleRate)
		require.Equal(t, test.soundType, tag.SoundType, "%d Hz", test.sampleRate)

		prober := NewProber(0)
		prober.HasAudio = true
		require.NoError(t, prober.PushTag(tag, 0))
		require.True(t, prober.Probed())
		require.Len(t, prober.Streams, 1)

		probed := prober.Streams[0].(av.AudioCodecData)
		require.Equal(t, av.NELLYMOSER, probed.Type())
		require.Equal(t, test.sampleRate, probed.SampleRate(), "%d Hz", test.sampleRate)
		require.Equal(t, test.channelLayout, probed.ChannelLayout(), "%d Hz", test.sampleRate)

		// 256 samples
		duration, err := probed.PacketDuration(make([]byte, 64))
		require.NoError(t, err)
		require.Equal(t, time.Duration(256)*time.Second/time.Duration(test.sampleRate), duration)

		pkt, ok := prober.TagToPacket(tag, 0)
		require.True(t, ok)
		require.Len(t, pkt.Data, 64)
	}
}

func TestProberSpeexNellymoserFrames(t *testing.T) {
	audioTags := map[string]flvio.Tag{
		"speex":      {Type: flvio.TAG_AUDIO, SoundFormat: flvio.SOUND_SPEEX},
		"nellymoser": {Type: flvio.TAG_AUDIO, SoundFormat: flvio.SOUND_NELLYMOSER_16KHZ_MONO},
	}

	for name, audio := range audioTags {
		// the audio frames arrive while probing still waits for the video
		prober := NewProber(0)
		prober.HasAudio = true
		prober.HasVideo = true

		for i := 0; i < 3; i++ {
			audio.Data = []byte{byte(i)}
			require.NoError(t, prober.PushTag(audio, int64(20*i)))
		}
		require.False(t, prober.Probed())

		require.NoError(t, prober.PushTag(flvio.Tag{
			Type:       flvio.TAG_VIDEO,
			IsExHeader: true,
			FrameType:  flvio.FRAME_KEY,
			PacketType: flvio.PKTTYPE_SEQUENCE_START,
			FourCC:     flvio.FOURCC_VVC,
			Data:       []byte{0x01, 0x02, 0x03, 0x04},
		}, 60))
		require.True(t, prober.Probed())

		for i := 0; i < 3; i++ {
			require.False(t, prober.Empty(), name)
			pkt := prober.PopPacket()
			require.Equal(t, int8(prober.AudioStreamIdx), pkt.Idx, name)
			require.Equal(t, []byte{byte(i)}, pkt.Data, name)
			require.Equal(t, int64(20*i), pkt.Time, name)
		}
		require.True(t, prober.Empty(), name)
	}
}

func TestProberPushMetadata(t *testing.T) {
	tests := []struct {
		metadata flvio.AMFMap
		hasVideo bool
		hasAudio bool
	}{
		{flvio.AMFMap{}, false, false},
		{flvio.AMFMap{"videocodecid": 7.0, "audiocodecid": 10.0}, true, true},
		{flvio.AMFMap{"videocodecid": "avc1", "audiocodecid": "mp4a"}, true, true},
		{flvio.AMFMap{"videocodecid": flvio.FourCCToFloat(flvio.FOURCC_HEVC)}, true, false},
		{flvio.AMFMap{"videocodecid": flvio.FourCCToFloat(flvio.FOURCC_VP9)}, true, false},
		{flvio.AMFMap{"videocodecid": flvio.FourCCToFloat(flvio.FOURCC_AV1)}, true, false},
		{flvio.AMFMap{"videocodecid": flvio.FourCCToFloat(flvio.FOURCC_VVC)}, true, false},
		{flvio.AMFMap{"videocodecid": "hvc1"}, true, false},
		{flvio.AMFMap{"audiocodecid": 11.0}, false, true},
		{flvio.AMFMap{"audiocodecid": 4.0}, false, true},
		{flvio.AMFMap{"audiocodecid": 5.0}, false, true},
		{flvio.AMFMap{"audiocodecid": 6.0}, false, true},
		// unsupported: H.263, VP6, linear PCM, ADPCM, an unknown FourCC
		{flvio.AMFMap{"videocodecid": 2.0, "audiocodecid": 3.0}, false, false},
		{flvio.AMFMap{"videocodecid": 4.0, "audiocodecid": 1.0}, false, false},
		{flvio.AMFMap{"videocodecid": "VP62", "audiocodecid": 0.0}, false, false},
		{flvio.AMFMap{"audiocodecid": "Opus"}, false, true},
		{flvio.AMFMap{"audiocodecid": flvio.FourCCToFloat(flvio.FOURCC_OPUS)}, false, true},
		{flvio.AMFMap{"audiocodecid": "fLaC"}, false, true},
		{flvio.AMFMap{"audiocodecid": flvio.FourCCToFloat(flvio.FOURCC_FLAC)}, false, true},
		{flvio.AMFMap{"audiocodecid": "ac-3"}, false, true},
		{flvio.AMFMap{"audiocodecid": flvio.FourCCToFloat(flvio.FOURCC_AC3)}, false, true},
		{flvio.AMFMap{"audiocodecid": "ec-3"}, false, true},
		{flvio.AMFMap{"audiocodecid": flvio.FourCCToFloat(flvio.FOURCC_EAC3)}, false, true},
		{flvio.AMFMap{"audiocodecid": 2.0}, false, true},
		{flvio.AMFMap{"audiocodecid": 14.0}, false, true},
		{flvio.AMFMap{"audiocodecid": ".mp3"}, false, true},
		{flvio.AMFMap{"audiocodecid": flvio.FourCCToFloat(flvio.FOURCC_MP3)}, false, true},
		{flvio.AMFMap{"audiocodecid": "abcd"}, false, false},
		{flvio.AMFMap{"videocodecid": nil, "audiocodecid": true}, false, false},
	}

	for _, test := range tests {
		prober := NewProber(0)
		prober.PushMetadata(test.metadata)
		require.Equal(t, test.hasVideo, prober.HasVideo, "%v", test.metadata)
		require.Equal(t, test.hasAudio, prober.HasAudio, "%v", test.metadata)
	}
}

func TestProberErr(t *testing.T) {
	prober := NewProber(0)
	prober.HasVideo = true
	require.NoError(t, prober.Err())

	err := prober.PushTag(flvio.Tag{Type: flvio.TAG_VIDEO, CodecID: flvio.VIDEO_H264, AVCPacketType: flvio.AVC_SEQHDR, Data: []byte{0xFF}}, 0)
	require.Error(t, err)
	require.Equal(t, err, prober.Err())

	// a later valid tag doesn't clear the error
	require.NoError(t, prober.PushTag(flvio.Tag{Type: flvio.TAG_AUDIO, SoundFormat: flvio.SOUND_SPEEX}, 0))
	require.Equal(t, err, prober.Err())
}

func TestAACTagFlags(t *testing.T) {
	for _, channels := range []uint{1, 2, 6} {
		stream, err := aacparser.NewCodecDataFromMPEG4AudioConfig(aacparser.MPEG4AudioConfig{
			ObjectType:    aacparser.AOT_AAC_LC,
			SampleRate:    48000,
			ChannelConfig: channels,
		})
		require.NoError(t, err)
		require.Equal(t, int(channels), stream.ChannelLayout().Count())

		// AAC, 44 kHz, 16 bit, stereo, as required by the FLV spec
		b := make([]byte, 16)

		tag, ok, err := CodecDataToTag(stream)
		require.NoError(t, err)
		require.True(t, ok)
		n := tag.FillHeader(b)
		require.Equal(t, []byte{0xAF, flvio.AAC_SEQHDR}, b[:n], "%d channels", channels)

		tag, _ = PacketToTag(av.Packet{Data: []byte{0x01}}, stream)
		n = tag.FillHeader(b)
		require.Equal(t, []byte{0xAF, flvio.AAC_RAW}, b[:n], "%d channels", channels)

		// the channels are taken from the AudioSpecificConfig
		buf := &bytes.Buffer{}
		muxer := NewMuxer(buf)
		require.NoError(t, muxer.WriteHeader([]av.CodecData{stream}))
		require.NoError(t, muxer.WritePacket(av.Packet{Data: []byte{0x01}}))
		require.NoError(t, muxer.WriteTrailer())

		streams, err := NewDemuxer(bytes.NewReader(buf.Bytes())).Streams()
		require.NoError(t, err)
		require.Len(t, streams, 1)
		require.Equal(t, int(channels), streams[0].(av.AudioCodecData).ChannelLayout().Count())
		require.Equal(t, 48000, streams[0].(av.AudioCodecData).SampleRate())
	}
}

// H.264 config records of FFmpeg (libx264) with 320x240 and 640x360
const (
	avcRecord320x240 = "01f4000dffe1001867f4000d91968141fb011000000300100000030320f142aa01000568ce0f1920fff8f800"
	avcRecord640x360 = "01f4001effe1001967f4001e919680a02ff89c0440000003004000000c83c58ba801000568ce0f1920fff8f800"
)

// HEVC parameter sets of FFmpeg (libx265) with 320x240 and 640x360: VPS, SPS, PPS
var (
	hevcParamSets320x240 = []string{
		"40010c01ffff0408000003009e0800000300003c959409",
		"4201010408000003009e0800000300003c90014101e2cb2b2a5261780b7020200040000003004000000642",
		"4401c07318301890",
	}
	hevcParamSets640x360 = []string{
		"40010c01ffff0408000003009e0800000300003f959409",
		"4201010408000003009e0800000300003f9000a0402e3e272caca94985e02dc08080010000030001000003001908",
		"4401c07318301890",
	}
)

func decodeHex(t *testing.T, s string) []byte {
	b, err := hex.DecodeString(s)
	require.NoError(t, err)
	return b
}

func hevcRecord(t *testing.T, paramSets []string) []byte {
	codecData, err := hevcparser.NewCodecDataFromVPSAndSPSAndPPS(decodeHex(t, paramSets[0]), decodeHex(t, paramSets[1]), decodeHex(t, paramSets[2]))
	require.NoError(t, err)
	return codecData.HEVCDecoderConfRecordBytes()
}

// avcTag returns a legacy H.264 tag as flvio parses it.
func avcTag(avcPacketType uint8, frameType uint8, data []byte) flvio.Tag {
	tag := flvio.Tag{Type: flvio.TAG_VIDEO, CodecID: flvio.VIDEO_H264, FrameType: frameType, AVCPacketType: avcPacketType, Data: data}
	if avcPacketType == flvio.AVC_SEQHDR {
		tag.PacketType = flvio.PKTTYPE_SEQUENCE_START
	} else {
		tag.PacketType = flvio.PKTTYPE_CODED_FRAMES
	}
	return tag
}

// withParamSets returns the frame with the length-prefixed (4 bytes) NALUs in front of it.
func withParamSets(frame []byte, nalus ...[]byte) []byte {
	var b []byte
	for _, nalu := range nalus {
		b = append(b, byte(len(nalu)>>24), byte(len(nalu)>>16), byte(len(nalu)>>8), byte(len(nalu)))
		b = append(b, nalu...)
	}
	return append(b, frame...)
}

func TestProberMidStreamSequenceHeaderH264(t *testing.T) {
	recordA := decodeHex(t, avcRecord320x240)
	recordB := decodeHex(t, avcRecord640x360)
	codecDataB, err := h264parser.NewCodecDataFromAVCDecoderConfRecord(recordB)
	require.NoError(t, err)

	keyFrame := []byte{0x00, 0x00, 0x00, 0x02, 0x65, 0x88}
	interFrame := []byte{0x00, 0x00, 0x00, 0x02, 0x41, 0x9a}

	prober := NewProber(0)
	prober.HasVideo = true
	require.NoError(t, prober.PushTag(avcTag(flvio.AVC_SEQHDR, flvio.FRAME_KEY, recordA), 0))
	pushCodedFrame(t, prober)
	require.True(t, prober.Probed())
	require.Len(t, prober.Streams, 1)

	frame := func(frameType uint8, data []byte) []byte {
		pkt, ok := prober.TagToPacket(avcTag(flvio.AVC_NALU, frameType, data), 0)
		require.True(t, ok)
		return pkt.Data
	}
	seqhdr := func(record []byte) {
		_, ok := prober.TagToPacket(avcTag(flvio.AVC_SEQHDR, flvio.FRAME_KEY, record), 0)
		require.False(t, ok)
	}

	require.Equal(t, keyFrame, frame(flvio.FRAME_KEY, keyFrame))

	// a re-sent, identical config changes nothing
	seqhdr(append([]byte(nil), recordA...))
	require.Equal(t, keyFrame, frame(flvio.FRAME_KEY, keyFrame))

	// a new config is put in front of every following key frame, not in front of inter frames
	seqhdr(recordB)
	expected := withParamSets(keyFrame, codecDataB.SPS(), codecDataB.PPS())
	require.Equal(t, expected, frame(flvio.FRAME_KEY, keyFrame))
	require.Equal(t, interFrame, frame(flvio.FRAME_INTER, interFrame))
	require.Equal(t, expected, frame(flvio.FRAME_KEY, keyFrame))

	// a re-sent new config doesn't change that
	seqhdr(append([]byte(nil), recordB...))
	require.Equal(t, expected, frame(flvio.FRAME_KEY, keyFrame))

	// the stream keeps the first config
	require.Equal(t, 320, prober.Streams[0].(av.VideoCodecData).Width())

	// back to the config of the stream
	seqhdr(recordA)
	require.Equal(t, keyFrame, frame(flvio.FRAME_KEY, keyFrame))
}

func TestProberMidStreamSequenceHeaderWhileProbing(t *testing.T) {
	recordA := decodeHex(t, avcRecord320x240)
	recordB := decodeHex(t, avcRecord640x360)
	codecDataB, err := h264parser.NewCodecDataFromAVCDecoderConfRecord(recordB)
	require.NoError(t, err)

	keyFrame := []byte{0x00, 0x00, 0x00, 0x02, 0x65, 0x88}

	prober := NewProber(0)
	prober.HasAudio = true
	prober.HasVideo = true
	require.NoError(t, prober.PushTag(avcTag(flvio.AVC_SEQHDR, flvio.FRAME_KEY, recordA), 0))
	require.NoError(t, prober.PushTag(avcTag(flvio.AVC_NALU, flvio.FRAME_KEY, keyFrame), 0))
	require.NoError(t, prober.PushTag(avcTag(flvio.AVC_SEQHDR, flvio.FRAME_KEY, recordB), 40))
	require.NoError(t, prober.PushTag(avcTag(flvio.AVC_NALU, flvio.FRAME_KEY, keyFrame), 40))
	require.False(t, prober.Probed())
	require.NoError(t, prober.PushTag(aacTag(flvio.AAC_SEQHDR, []byte{0x12, 0x10}), 40))
	require.True(t, prober.Probed())
	require.Len(t, prober.Streams, 2)

	require.False(t, prober.Empty())
	require.Equal(t, keyFrame, prober.PopPacket().Data)
	require.False(t, prober.Empty())
	require.Equal(t, withParamSets(keyFrame, codecDataB.SPS(), codecDataB.PPS()), prober.PopPacket().Data)
	require.True(t, prober.Empty())
}

func TestProberMidStreamSequenceHeaderHEVC(t *testing.T) {
	recordA := hevcRecord(t, hevcParamSets320x240)
	recordB := hevcRecord(t, hevcParamSets640x360)

	keyFrame := []byte{0x00, 0x00, 0x00, 0x03, 0x26, 0x01, 0xaf}

	prober := NewProber(0)
	prober.HasVideo = true
	require.NoError(t, prober.PushTag(exVideoTag(flvio.FOURCC_HEVC, flvio.PKTTYPE_SEQUENCE_START, recordA), 0))
	pushCodedFrame(t, prober)
	require.True(t, prober.Probed())

	_, ok := prober.TagToPacket(exVideoTag(flvio.FOURCC_HEVC, flvio.PKTTYPE_SEQUENCE_START, recordB), 0)
	require.False(t, ok)

	pkt, ok := prober.TagToPacket(exVideoTag(flvio.FOURCC_HEVC, flvio.PKTTYPE_CODED_FRAMESX, keyFrame), 0)
	require.True(t, ok)
	require.Equal(t, withParamSets(keyFrame, decodeHex(t, hevcParamSets640x360[0]), decodeHex(t, hevcParamSets640x360[1]), decodeHex(t, hevcParamSets640x360[2])), pkt.Data)
}

func TestProberMidStreamSequenceHeaderIgnored(t *testing.T) {
	recordA := decodeHex(t, avcRecord320x240)

	// NALU length size 2 instead of 4
	otherLengthSize := decodeHex(t, avcRecord640x360)
	otherLengthSize[4] = 0xfd

	keyFrame := []byte{0x00, 0x00, 0x00, 0x02, 0x65, 0x88}

	for name, tag := range map[string]flvio.Tag{
		"invalid":           avcTag(flvio.AVC_SEQHDR, flvio.FRAME_KEY, []byte{0x01, 0x02}),
		"other length size": avcTag(flvio.AVC_SEQHDR, flvio.FRAME_KEY, otherLengthSize),
		"other codec":       exVideoTag(flvio.FOURCC_HEVC, flvio.PKTTYPE_SEQUENCE_START, hevcRecord(t, hevcParamSets640x360)),
	} {
		prober := NewProber(0)
		prober.HasVideo = true
		require.NoError(t, prober.PushTag(avcTag(flvio.AVC_SEQHDR, flvio.FRAME_KEY, recordA), 0), name)
		pushCodedFrame(t, prober)
		require.True(t, prober.Probed(), name)

		_, ok := prober.TagToPacket(tag, 0)
		require.False(t, ok, name)

		pkt, ok := prober.TagToPacket(avcTag(flvio.AVC_NALU, flvio.FRAME_KEY, keyFrame), 0)
		require.True(t, ok, name)
		require.Equal(t, keyFrame, pkt.Data, name)
	}
}

func exAudioTag(fourcc [4]byte, packetType uint8, data []byte) flvio.Tag {
	return flvio.Tag{Type: flvio.TAG_AUDIO, SoundFormat: flvio.SOUND_EXHEADER, IsExHeader: true, PacketType: packetType, FourCC: fourcc, Data: data}
}

func TestProberExAudioAAC(t *testing.T) {
	prober := NewProber(0)
	prober.PushMetadata(flvio.AMFMap{"audiocodecid": flvio.FourCCToFloat(flvio.FOURCC_AAC)})
	require.True(t, prober.HasAudio)

	// a frame before the sequence start is cached
	require.NoError(t, prober.PushTag(exAudioTag(flvio.FOURCC_AAC, flvio.PKTTYPE_CODED_FRAMES, []byte{0xA0}), 0))
	require.False(t, prober.Probed())
	require.NoError(t, prober.PushTag(exAudioTag(flvio.FOURCC_AAC, flvio.PKTTYPE_SEQUENCE_START, []byte{0x12, 0x10}), 0))
	require.True(t, prober.Probed())
	require.Len(t, prober.Streams, 1)
	stream, ok := prober.Streams[0].(aacparser.CodecData)
	require.True(t, ok)
	require.Equal(t, 44100, stream.SampleRate())

	require.False(t, prober.Empty())
	pkt := prober.PopPacket()
	require.Equal(t, []byte{0xA0}, pkt.Data)
	require.True(t, prober.Empty())

	pkt, ok = prober.TagToPacket(exAudioTag(flvio.FOURCC_AAC, flvio.PKTTYPE_CODED_FRAMES, []byte{0xA1}), 23)
	require.True(t, ok)
	require.Equal(t, []byte{0xA1}, pkt.Data)
	require.Equal(t, int64(23), pkt.Time)
	require.True(t, pkt.IsKeyFrame)

	// a legacy AAC frame belongs to the same stream
	pkt, ok = prober.TagToPacket(aacTag(flvio.AAC_RAW, []byte{0xA2}), 46)
	require.True(t, ok)
	require.Equal(t, []byte{0xA2}, pkt.Data)

	// other packet types and codecs are not passed on
	for _, tag := range []flvio.Tag{
		exAudioTag(flvio.FOURCC_AAC, flvio.PKTTYPE_SEQUENCE_START, []byte{0x12, 0x10}),
		exAudioTag(flvio.FOURCC_AAC, flvio.AUDIO_PKTTYPE_MULTICHANNEL_CONFIG, []byte{0x01, 0x02, 0x00, 0x00, 0x00, 0x03}),
		exAudioTag(flvio.FOURCC_AAC, flvio.PKTTYPE_SEQUENCE_END, nil),
		exAudioTag(flvio.FOURCC_OPUS, flvio.PKTTYPE_CODED_FRAMES, []byte{0xA3}),
	} {
		_, ok = prober.TagToPacket(tag, 69)
		require.False(t, ok)
	}
}

// unknownFourCC is the FourCC of a codec that isn't supported.
var unknownFourCC = [4]byte{'a', 'b', 'c', 'd'}

func TestProberExAudioUnsupportedCodec(t *testing.T) {
	// an unknown codec isn't supported: the metadata doesn't enable audio and its frames are dropped
	prober := NewProber(0)
	prober.PushMetadata(flvio.AMFMap{
		"videocodecid": flvio.FourCCToFloat(flvio.FOURCC_VVC),
		"audiocodecid": flvio.FourCCToFloat(unknownFourCC),
	})
	require.True(t, prober.HasVideo)
	require.False(t, prober.HasAudio)

	require.NoError(t, prober.PushTag(exAudioTag(unknownFourCC, flvio.PKTTYPE_SEQUENCE_START, []byte{0x01}), 0))
	require.NoError(t, prober.PushTag(exAudioTag(unknownFourCC, flvio.PKTTYPE_CODED_FRAMES, []byte{0xA0}), 0))
	require.NoError(t, prober.PushTag(exVideoTag(flvio.FOURCC_VVC, flvio.PKTTYPE_SEQUENCE_START, []byte{0x01}), 0))
	require.True(t, prober.Probed())
	require.Len(t, prober.Streams, 1)
	require.Equal(t, av.VVC, prober.Streams[0].Type())
	require.True(t, prober.Empty())
}

// flvTag returns an FLV tag with the data, including the header and the previous tag size.
func flvTag(tagType uint8, timestamp int64, data []byte) []byte {
	b := make([]byte, flvio.TagHeaderLength)
	flvio.FillTagHeader(b, tagType, len(data), timestamp)
	b = append(b, data...)
	return append(b, byte((len(data)+flvio.TagHeaderLength)>>24), byte((len(data)+flvio.TagHeaderLength)>>16), byte((len(data)+flvio.TagHeaderLength)>>8), byte(len(data)+flvio.TagHeaderLength))
}

func TestDemuxerMultitrack(t *testing.T) {
	recordA := decodeHex(t, avcRecord320x240)
	recordB := decodeHex(t, avcRecord640x360)

	file := make([]byte, flvio.FileHeaderLength+4)
	flvio.FillFileHeader(file, flvio.FILE_HAS_AUDIO|flvio.FILE_HAS_VIDEO)
	for _, tag := range [][]byte{
		// track 0 with the legacy header, track 1 as OneTrack, as FFmpeg writes them
		flvTag(flvio.TAG_VIDEO, 0, append([]byte{0x17, 0x00, 0x00, 0x00, 0x00}, recordA...)),
		flvTag(flvio.TAG_VIDEO, 0, append([]byte{0x96, 0x00, 'a', 'v', 'c', '1', 0x01}, recordB...)),
		flvTag(flvio.TAG_AUDIO, 0, []byte{0xAF, 0x00, 0x12, 0x10}),
		flvTag(flvio.TAG_AUDIO, 0, []byte{0x95, 0x00, 'm', 'p', '4', 'a', 0x01, 0x11, 0x88}),
		// both tracks in one message
		flvTag(flvio.TAG_VIDEO, 0, []byte{0x96, 0x13, 'a', 'v', 'c', '1', 0x00, 0x00, 0x00, 0x01, 0xA0, 0x01, 0x00, 0x00, 0x01, 0xB0}),
		flvTag(flvio.TAG_AUDIO, 0, []byte{0x95, 0x11, 'm', 'p', '4', 'a', 0x00, 0x00, 0x00, 0x01, 0xAA, 0x01, 0x00, 0x00, 0x01, 0xBB}),
		// track 0 as OneTrack, and AVC with the ExVideoTagHeader and a composition time
		flvTag(flvio.TAG_VIDEO, 40, []byte{0x96, 0x01, 'a', 'v', 'c', '1', 0x00, 0x00, 0x00, 0x28, 0xA1}),
		flvTag(flvio.TAG_VIDEO, 40, []byte{0x96, 0x01, 'a', 'v', 'c', '1', 0x01, 0x00, 0x00, 0x28, 0xB1}),
		flvTag(flvio.TAG_VIDEO, 80, []byte{0x91, 'a', 'v', 'c', '1', 0x00, 0x00, 0x14, 0xA2}),
	} {
		file = append(file, tag...)
	}

	// every track is a stream, probing ends at the first coded frame
	demuxer := NewDemuxer(bytes.NewReader(file))
	streams, err := demuxer.Streams()
	require.NoError(t, err)
	require.Len(t, streams, 4)
	require.Equal(t, av.H264, streams[0].Type())
	require.Equal(t, 320, streams[0].(av.VideoCodecData).Width())
	require.Equal(t, av.H264, streams[1].Type())
	require.Equal(t, 640, streams[1].(av.VideoCodecData).Width())
	require.Equal(t, av.AAC, streams[2].Type())
	require.Equal(t, 44100, streams[2].(av.AudioCodecData).SampleRate())
	require.Equal(t, av.AAC, streams[3].Type())
	require.Equal(t, 48000, streams[3].(av.AudioCodecData).SampleRate())

	for i, expected := range []av.Packet{
		{Idx: 0, Data: []byte{0xA0}, IsKeyFrame: true},
		{Idx: 1, Data: []byte{0xB0}, IsKeyFrame: true},
		{Idx: 2, Data: []byte{0xAA}, IsKeyFrame: true},
		{Idx: 3, Data: []byte{0xBB}, IsKeyFrame: true},
		{Idx: 0, Data: []byte{0xA1}, IsKeyFrame: true, Time: 40, CompositionTime: 40},
		{Idx: 1, Data: []byte{0xB1}, IsKeyFrame: true, Time: 40, CompositionTime: 40},
		{Idx: 0, Data: []byte{0xA2}, IsKeyFrame: true, Time: 80, CompositionTime: 20},
	} {
		pkt, err := demuxer.ReadPacket()
		require.NoError(t, err, "packet %d", i)
		require.Equal(t, expected, pkt, "packet %d", i)
	}
	_, err = demuxer.ReadPacket()
	require.Error(t, err)
}

// trackTag returns the tag as a OneTrack multitrack tag.
func trackTag(tag flvio.Tag, trackID uint8) flvio.Tag {
	return TrackTag(tag, trackID)
}

func TestProberTrackIdInfoMap(t *testing.T) {
	recordA := decodeHex(t, avcRecord320x240)
	recordB := decodeHex(t, avcRecord640x360)

	prober := NewProber(0)
	prober.PushMetadata(flvio.AMFMap{
		"videocodecid": 7.0,
		"audiocodecid": 10.0,
		"videoTrackIdInfoMap": flvio.AMFMap{
			// delta-style: the codec of the default track
			"1": flvio.AMFMap{"width": 640.0},
			// unsupported, not waited for
			"2": flvio.AMFMap{"videocodecid": flvio.FourCCToFloat([4]byte{'v', 'p', '0', '8'})},
		},
		"audioTrackIdInfoMap": flvio.AMFMap{
			"1": flvio.AMFMap{"audiocodecid": flvio.FourCCToFloat(flvio.FOURCC_AAC)},
		},
	})

	require.NoError(t, prober.PushTag(avcTag(flvio.AVC_SEQHDR, flvio.FRAME_KEY, recordA), 0))
	require.NoError(t, prober.PushTag(aacTag(flvio.AAC_SEQHDR, []byte{0x12, 0x10}), 0))
	require.NoError(t, prober.PushTag(trackTag(exAudioTag(flvio.FOURCC_AAC, flvio.PKTTYPE_SEQUENCE_START, []byte{0x11, 0x88}), 1), 0))
	require.False(t, prober.Probed())
	require.NoError(t, prober.PushTag(trackTag(avcTag(flvio.AVC_SEQHDR, flvio.FRAME_KEY, recordB), 1), 0))
	// all announced tracks are there, no coded frame needed
	require.True(t, prober.Probed())

	require.Len(t, prober.Streams, 4)
	require.Equal(t, 320, prober.Streams[0].(av.VideoCodecData).Width())
	require.Equal(t, 44100, prober.Streams[1].(av.AudioCodecData).SampleRate())
	require.Equal(t, 48000, prober.Streams[2].(av.AudioCodecData).SampleRate())
	require.Equal(t, 640, prober.Streams[3].(av.VideoCodecData).Width())
	require.Equal(t, 0, prober.VideoStreamIdx)
	require.Equal(t, 1, prober.AudioStreamIdx)

	keyFrame := []byte{0x00, 0x00, 0x00, 0x02, 0x65, 0x88}
	for _, tc := range []struct {
		tag flvio.Tag
		idx int8
	}{
		{avcTag(flvio.AVC_NALU, flvio.FRAME_KEY, keyFrame), 0},
		{trackTag(avcTag(flvio.AVC_NALU, flvio.FRAME_KEY, keyFrame), 1), 3},
		{aacTag(flvio.AAC_RAW, []byte{0xAA}), 1},
		{trackTag(exAudioTag(flvio.FOURCC_AAC, flvio.PKTTYPE_CODED_FRAMES, []byte{0xBB}), 1), 2},
	} {
		pkt, ok := prober.TagToPacket(tc.tag, 0)
		require.True(t, ok)
		require.Equal(t, tc.idx, pkt.Idx)
	}

	// a track that appears after probing is dropped
	_, ok := prober.TagToPacket(trackTag(avcTag(flvio.AVC_NALU, flvio.FRAME_KEY, keyFrame), 3), 0)
	require.False(t, ok)
}

func TestProberTracksSorted(t *testing.T) {
	// without a TrackIdInfoMap, probing ends at the first coded frame, the tracks are sorted by their id
	prober := NewProber(0)
	prober.PushMetadata(flvio.AMFMap{"videocodecid": flvio.FourCCToFloat(flvio.FOURCC_VVC)})

	require.NoError(t, prober.PushTag(trackTag(exVideoTag(flvio.FOURCC_VVC, flvio.PKTTYPE_SEQUENCE_START, []byte{0x02}), 2), 0))
	require.NoError(t, prober.PushTag(exVideoTag(flvio.FOURCC_VVC, flvio.PKTTYPE_SEQUENCE_START, []byte{0x00}), 0))
	require.False(t, prober.Probed())
	require.NoError(t, prober.PushTag(trackTag(exVideoTag(flvio.FOURCC_HEVC, flvio.PKTTYPE_SEQUENCE_START, hevcRecord(t, hevcParamSets320x240)), 1), 0))
	require.False(t, prober.Probed())
	require.NoError(t, prober.PushTag(trackTag(exVideoTag(flvio.FOURCC_VVC, flvio.PKTTYPE_CODED_FRAMESX, []byte{0xC0}), 2), 0))
	require.True(t, prober.Probed())

	require.Len(t, prober.Streams, 3)
	require.Equal(t, []byte{0x00}, prober.Streams[0].(vvcparser.CodecData).VVCDecoderConfRecordBytes())
	require.Equal(t, av.HEVC, prober.Streams[1].Type())
	require.Equal(t, []byte{0x02}, prober.Streams[2].(vvcparser.CodecData).VVCDecoderConfRecordBytes())
	require.Equal(t, 0, prober.VideoStreamIdx)

	require.False(t, prober.Empty())
	pkt := prober.PopPacket()
	require.Equal(t, int8(2), pkt.Idx)
	require.Equal(t, []byte{0xC0}, pkt.Data)
	require.True(t, prober.Empty())
}

func TestProberTracksMidStreamSequenceHeader(t *testing.T) {
	// the in-band parameter sets of a new config are per track
	recordA := decodeHex(t, avcRecord320x240)
	recordB := decodeHex(t, avcRecord640x360)
	codecDataB, err := h264parser.NewCodecDataFromAVCDecoderConfRecord(recordB)
	require.NoError(t, err)
	keyFrame := []byte{0x00, 0x00, 0x00, 0x02, 0x65, 0x88}

	prober := NewProber(0)
	prober.PushMetadata(flvio.AMFMap{"videocodecid": 7.0, "videoTrackIdInfoMap": flvio.AMFMap{"1": flvio.AMFMap{}}})
	require.NoError(t, prober.PushTag(avcTag(flvio.AVC_SEQHDR, flvio.FRAME_KEY, recordA), 0))
	require.NoError(t, prober.PushTag(trackTag(avcTag(flvio.AVC_SEQHDR, flvio.FRAME_KEY, recordA), 1), 0))
	require.True(t, prober.Probed())

	_, ok := prober.TagToPacket(trackTag(avcTag(flvio.AVC_SEQHDR, flvio.FRAME_KEY, recordB), 1), 0)
	require.False(t, ok)

	pkt, ok := prober.TagToPacket(avcTag(flvio.AVC_NALU, flvio.FRAME_KEY, keyFrame), 0)
	require.True(t, ok)
	require.Equal(t, keyFrame, pkt.Data)

	pkt, ok = prober.TagToPacket(trackTag(avcTag(flvio.AVC_NALU, flvio.FRAME_KEY, keyFrame), 1), 0)
	require.True(t, ok)
	require.Equal(t, int8(1), pkt.Idx)
	require.Equal(t, withParamSets(keyFrame, codecDataB.SPS(), codecDataB.PPS()), pkt.Data)
}

func TestProberTracksColorInfo(t *testing.T) {
	// only the color info of the default video track is used
	info0 := flvio.AMFMap{"colorConfig": flvio.AMFMap{"colorPrimaries": 9.0}}
	info1 := flvio.AMFMap{"colorConfig": flvio.AMFMap{"colorPrimaries": 1.0}}

	prober := NewProber(0)
	prober.PushMetadata(flvio.AMFMap{"videocodecid": flvio.FourCCToFloat(flvio.FOURCC_VVC), "videoTrackIdInfoMap": flvio.AMFMap{"1": flvio.AMFMap{}}})
	require.NoError(t, prober.PushTag(exVideoTag(flvio.FOURCC_VVC, flvio.PKTTYPE_SEQUENCE_START, []byte{0x00}), 0))
	require.NoError(t, prober.PushTag(trackTag(colorInfoTag(info1), 1), 0))
	require.NoError(t, prober.PushTag(trackTag(exVideoTag(flvio.FOURCC_VVC, flvio.PKTTYPE_SEQUENCE_START, []byte{0x01}), 1), 0))
	require.True(t, prober.Probed())
	require.True(t, prober.Empty())
	require.Nil(t, prober.ColorInfo)

	_, ok := prober.TagToPacket(colorInfoTag(info0), 0)
	require.False(t, ok)
	require.Equal(t, info0, prober.ColorInfo)
	_, ok = prober.TagToPacket(trackTag(colorInfoTag(info1), 1), 0)
	require.False(t, ok)
	require.Equal(t, info0, prober.ColorInfo)
}

func TestTrackIDs(t *testing.T) {
	h264, err := h264parser.NewCodecDataFromAVCDecoderConfRecord(decodeHex(t, avcRecord320x240))
	require.NoError(t, err)
	aac, err := aacparser.NewCodecDataFromMPEG4AudioConfigBytes([]byte{0x12, 0x10})
	require.NoError(t, err)
	speex := codec.NewSpeexCodecData(16000, av.CH_MONO)

	ids, err := TrackIDs([]av.CodecData{h264, aac, h264, aac, h264})
	require.NoError(t, err)
	require.Equal(t, []uint8{0, 0, 1, 1, 2}, ids)

	// Speex has no FourCC, it can only be the default track
	ids, err = TrackIDs([]av.CodecData{speex, aac})
	require.NoError(t, err)
	require.Equal(t, []uint8{0, 1}, ids)
	_, err = TrackIDs([]av.CodecData{aac, speex})
	require.Error(t, err)
}

func TestMuxDemuxTracks(t *testing.T) {
	h264, err := h264parser.NewCodecDataFromAVCDecoderConfRecord(decodeHex(t, avcRecord320x240))
	require.NoError(t, err)
	hevc, err := hevcparser.NewCodecDataFromHEVCDecoderConfRecord(hevcRecord(t, hevcParamSets640x360))
	require.NoError(t, err)
	aac44, err := aacparser.NewCodecDataFromMPEG4AudioConfigBytes([]byte{0x12, 0x10})
	require.NoError(t, err)
	aac48, err := aacparser.NewCodecDataFromMPEG4AudioConfigBytes([]byte{0x11, 0x88})
	require.NoError(t, err)
	streams := []av.CodecData{h264, aac44, hevc, aac48}

	packets := []av.Packet{
		{Idx: 0, IsKeyFrame: true, Data: []byte{0xA0}, CompositionTime: 40},
		{Idx: 2, IsKeyFrame: true, Data: []byte{0xB0}, CompositionTime: 40},
		{Idx: 1, IsKeyFrame: true, Data: []byte{0xAA}},
		{Idx: 3, IsKeyFrame: true, Data: []byte{0xBB}},
		{Idx: 0, Data: []byte{0xA1}, Time: 40},
		{Idx: 2, Data: []byte{0xB1}, Time: 40},
	}

	buf := &bytes.Buffer{}
	muxer := NewMuxer(buf)
	require.NoError(t, muxer.WriteHeader(streams))
	for _, pkt := range packets {
		require.NoError(t, muxer.WritePacket(pkt))
	}
	require.NoError(t, muxer.WriteTrailer())

	demuxer := NewDemuxer(bytes.NewReader(buf.Bytes()))
	got, err := demuxer.Streams()
	require.NoError(t, err)
	require.Equal(t, streams, got)
	for i, expected := range packets {
		pkt, err := demuxer.ReadPacket()
		require.NoError(t, err, "packet %d", i)
		require.Equal(t, expected, pkt, "packet %d", i)
	}

	// a stream that can't be a track
	require.Error(t, NewMuxer(&bytes.Buffer{}).WriteHeader([]av.CodecData{aac44, codec.NewSpeexCodecData(16000, av.CH_MONO)}))
}

func TestMetadataTracks(t *testing.T) {
	h264, err := h264parser.NewCodecDataFromAVCDecoderConfRecord(decodeHex(t, avcRecord320x240))
	require.NoError(t, err)
	h264b, err := h264parser.NewCodecDataFromAVCDecoderConfRecord(decodeHex(t, avcRecord640x360))
	require.NoError(t, err)
	aac44, err := aacparser.NewCodecDataFromMPEG4AudioConfigBytes([]byte{0x12, 0x10})
	require.NoError(t, err)
	aac48, err := aacparser.NewCodecDataFromMPEG4AudioConfigBytes([]byte{0x11, 0x88})
	require.NoError(t, err)

	metadata, err := NewMetadataByStreams([]av.CodecData{h264, aac44, h264b, aac48})
	require.NoError(t, err)
	require.Equal(t, flvio.AMFMap{
		"videocodecid":    flvio.VIDEO_H264,
		"width":           320,
		"height":          240,
		"displayWidth":    320,
		"displayHeight":   240,
		"audiocodecid":    flvio.SOUND_AAC,
		"audiosamplerate": 44100,
		"videoTrackIdInfoMap": flvio.AMFMap{
			"1": flvio.AMFMap{"videocodecid": flvio.FourCCToFloat(flvio.FOURCC_AVC), "width": 640, "height": 360},
		},
		"audioTrackIdInfoMap": flvio.AMFMap{
			"1": flvio.AMFMap{"audiocodecid": flvio.FourCCToFloat(flvio.FOURCC_AAC), "samplerate": 48000, "channels": 1},
		},
	}, metadata)

	// the prober waits for the announced tracks (the AMF encoding turns the numbers into float64)
	data := make([]byte, flvio.LenAMF0Val(metadata))
	flvio.FillAMF0Val(data, metadata)
	val, _, err := flvio.ParseAMF0Val(data)
	require.NoError(t, err)
	prober := NewProber(0)
	prober.PushMetadata(val.(flvio.AMFMap))
	require.True(t, prober.HasVideo)
	require.True(t, prober.HasAudio)
	require.Equal(t, map[uint8]bool{1: true}, prober.announcedVideoTracks)
	require.Equal(t, map[uint8]bool{1: true}, prober.announcedAudioTracks)
}

// the ID header of FFmpeg (libopus) for 5.1
const opusHead51 = "4f707573486561640106380180bb00000000010402000401020305"

func TestProberOpus(t *testing.T) {
	// as FFmpeg writes 5.1 Opus: the ID header, the multichannel config and the frames
	prober := NewProber(0)
	prober.PushMetadata(flvio.AMFMap{"audiocodecid": flvio.FourCCToFloat(flvio.FOURCC_OPUS)})
	require.True(t, prober.HasAudio)
	require.NoError(t, prober.PushTag(exAudioTag(flvio.FOURCC_OPUS, flvio.PKTTYPE_SEQUENCE_START, decodeHex(t, opusHead51)), 0))
	require.NoError(t, prober.PushTag(exAudioTag(flvio.FOURCC_OPUS, flvio.AUDIO_PKTTYPE_MULTICHANNEL_CONFIG, []byte{0x01, 0x06, 0x00, 0x00, 0x00, 0x3f}), 0))
	require.NoError(t, prober.PushTag(exAudioTag(flvio.FOURCC_OPUS, flvio.PKTTYPE_CODED_FRAMES, []byte{0xfc, 0xfc, 0x37}), 0))
	require.True(t, prober.Probed())
	require.Len(t, prober.Streams, 1)
	stream := prober.Streams[0].(opusparser.CodecData)
	require.Equal(t, 6, stream.ChannelLayout().Count())
	require.Equal(t, 48000, stream.SampleRate())
	require.Equal(t, decodeHex(t, opusHead51), stream.OpusHeadBytes())
	duration, err := stream.PacketDuration([]byte{0xfc, 0xfc, 0x37})
	require.NoError(t, err)
	require.Equal(t, 20*time.Millisecond, duration)

	require.False(t, prober.Empty())
	require.Equal(t, av.Packet{Data: []byte{0xfc, 0xfc, 0x37}, IsKeyFrame: true}, prober.PopPacket())
	require.True(t, prober.Empty())

	// without an ID header, the channels are taken from the multichannel config, or the first packet
	for _, tc := range []struct {
		name   string
		tag    flvio.Tag
		layout av.ChannelLayout
	}{
		{"multichannel config", exAudioTag(flvio.FOURCC_OPUS, flvio.AUDIO_PKTTYPE_MULTICHANNEL_CONFIG, []byte{0x01, 0x02, 0x00, 0x00, 0x00, 0x03}), av.CH_STEREO},
		{"stereo packet", exAudioTag(flvio.FOURCC_OPUS, flvio.PKTTYPE_CODED_FRAMES, []byte{0xfc, 0x00}), av.CH_STEREO},
		{"mono packet", exAudioTag(flvio.FOURCC_OPUS, flvio.PKTTYPE_CODED_FRAMES, []byte{0xf8, 0x00}), av.CH_MONO},
	} {
		prober := NewProber(0)
		prober.HasAudio = true
		require.NoError(t, prober.PushTag(exAudioTag(flvio.FOURCC_OPUS, flvio.PKTTYPE_SEQUENCE_START, nil), 0), tc.name)
		require.False(t, prober.GotAudio, tc.name)
		require.NoError(t, prober.PushTag(tc.tag, 0), tc.name)
		pushCodedFrame(t, prober)
		require.True(t, prober.Probed(), tc.name)
		require.Len(t, prober.Streams, 1, tc.name)
		require.Equal(t, tc.layout, prober.Streams[0].(av.AudioCodecData).ChannelLayout(), tc.name)
		require.Nil(t, prober.Streams[0].(opusparser.CodecData).Header, tc.name)
	}

	// an invalid ID header
	prober = NewProber(0)
	require.Error(t, prober.PushTag(exAudioTag(flvio.FOURCC_OPUS, flvio.PKTTYPE_SEQUENCE_START, []byte("OpusHead")), 0))
}

func TestMuxDemuxOpus(t *testing.T) {
	h264, err := h264parser.NewCodecDataFromAVCDecoderConfRecord(decodeHex(t, avcRecord320x240))
	require.NoError(t, err)
	opus51, err := opusparser.NewCodecDataFromOpusHead(decodeHex(t, opusHead51))
	require.NoError(t, err)
	opusStereo, err := opusparser.NewCodecData(2)
	require.NoError(t, err)

	// Opus as the default track and as track 1 (without an ID header)
	streams := []av.CodecData{h264, opus51, opusStereo}
	packets := []av.Packet{
		{Idx: 0, IsKeyFrame: true, Data: []byte{0xA0}},
		{Idx: 1, IsKeyFrame: true, Data: []byte{0xfc, 0xfc, 0x37}},
		{Idx: 2, IsKeyFrame: true, Data: []byte{0xfc, 0x01}},
		{Idx: 1, IsKeyFrame: true, Data: []byte{0xfc, 0xfd, 0x0e}, Time: 20},
	}

	buf := &bytes.Buffer{}
	muxer := NewMuxer(buf)
	require.NoError(t, muxer.WriteHeader(streams))
	for _, pkt := range packets {
		require.NoError(t, muxer.WritePacket(pkt))
	}
	require.NoError(t, muxer.WriteTrailer())

	// the stream without an ID header is written with a minimal one
	opusStereoHead, err := opusparser.NewCodecDataFromOpusHead(opusStereo.OpusHeadBytes())
	require.NoError(t, err)

	demuxer := NewDemuxer(bytes.NewReader(buf.Bytes()))
	got, err := demuxer.Streams()
	require.NoError(t, err)
	require.Equal(t, []av.CodecData{h264, opus51, opusStereoHead}, got)
	for i, expected := range packets {
		pkt, err := demuxer.ReadPacket()
		require.NoError(t, err, "packet %d", i)
		require.Equal(t, expected, pkt, "packet %d", i)
	}

	metadata, err := NewMetadataByStreams(streams)
	require.NoError(t, err)
	require.Equal(t, flvio.FourCCToFloat(flvio.FOURCC_OPUS), metadata["audiocodecid"])
	require.Equal(t, 48000, metadata["audiosamplerate"])
	require.Equal(t, flvio.AMFMap{"1": flvio.AMFMap{"audiocodecid": flvio.FourCCToFloat(flvio.FOURCC_OPUS), "samplerate": 48000, "channels": 2}}, metadata["audioTrackIdInfoMap"])
}

// the STREAMINFO of 48 kHz 5.1 16 bit FLAC, as FFmpeg writes it (without the "fLaC" marker), at the start
// and at the end of the stream (with the total samples, frame sizes and MD5)
const (
	flacStreamInfo51      = "040004000000000030240bb80af00000000000000000000000000000000000000000"
	flacStreamInfo51Final = "040004000000fc00018b0bb80af0000232800e9d7dce5ba2b3c21e0da827578eebfe"
)

func TestProberFLAC(t *testing.T) {
	// as FFmpeg writes FLAC: the STREAMINFO, the multichannel config and the frames, and at the end the
	// final STREAMINFO, the multichannel config and an empty frame
	frame := decodeHex(t, "fff8aa5800e14c000000eb")
	lastFrame := decodeHex(t, "fff87a58c28c027f7c4c0b")

	prober := NewProber(0)
	prober.PushMetadata(flvio.AMFMap{"audiocodecid": flvio.FourCCToFloat(flvio.FOURCC_FLAC)})
	require.True(t, prober.HasAudio)
	// a frame before the sequence start creates no stream, it is passed on with the stream
	require.NoError(t, prober.PushTag(exAudioTag(flvio.FOURCC_FLAC, flvio.PKTTYPE_CODED_FRAMES, frame), 0))
	require.False(t, prober.GotAudio)
	require.False(t, prober.Probed())
	require.NoError(t, prober.PushTag(exAudioTag(flvio.FOURCC_FLAC, flvio.PKTTYPE_SEQUENCE_START, decodeHex(t, flacStreamInfo51)), 0))
	require.NoError(t, prober.PushTag(exAudioTag(flvio.FOURCC_FLAC, flvio.AUDIO_PKTTYPE_MULTICHANNEL_CONFIG, []byte{0x01, 0x06, 0x00, 0x00, 0x00, 0x3f}), 0))
	require.NoError(t, prober.PushTag(exAudioTag(flvio.FOURCC_FLAC, flvio.PKTTYPE_CODED_FRAMES, frame), 21))
	require.True(t, prober.Probed())
	require.Len(t, prober.Streams, 1)
	stream := prober.Streams[0].(flacparser.CodecData)
	require.Equal(t, av.FLAC, stream.Type())
	require.Equal(t, 6, stream.ChannelLayout().Count())
	require.Equal(t, 48000, stream.SampleRate())
	require.Equal(t, 16, stream.BitsPerSample)
	require.Equal(t, decodeHex(t, flacStreamInfo51), stream.StreamInfoBytes())
	duration, err := stream.PacketDuration(frame)
	require.NoError(t, err)
	require.Equal(t, 1024*time.Second/48000, duration)

	require.Equal(t, av.Packet{Data: frame, IsKeyFrame: true}, prober.PopPacket())
	require.Equal(t, av.Packet{Data: frame, IsKeyFrame: true, Time: 21}, prober.PopPacket())
	require.True(t, prober.Empty())

	// after probing: the last frame is passed on, the final STREAMINFO, the multichannel config and the
	// empty frame are not
	for _, tag := range []flvio.Tag{
		exAudioTag(flvio.FOURCC_FLAC, flvio.PKTTYPE_CODED_FRAMES, lastFrame),
		exAudioTag(flvio.FOURCC_FLAC, flvio.PKTTYPE_SEQUENCE_START, decodeHex(t, flacStreamInfo51Final)),
		exAudioTag(flvio.FOURCC_FLAC, flvio.AUDIO_PKTTYPE_MULTICHANNEL_CONFIG, []byte{0x01, 0x06, 0x00, 0x00, 0x00, 0x3f}),
		exAudioTag(flvio.FOURCC_FLAC, flvio.PKTTYPE_CODED_FRAMES, []byte{}),
	} {
		pkt, ok := prober.TagToPacket(tag, 3000)
		if tag.PacketType == flvio.PKTTYPE_CODED_FRAMES && len(tag.Data) != 0 {
			require.True(t, ok)
			require.Equal(t, av.Packet{Data: lastFrame, IsKeyFrame: true, Time: 3000}, pkt)
			duration, err := stream.PacketDuration(pkt.Data)
			require.NoError(t, err)
			require.Equal(t, 640*time.Second/48000, duration)
			continue
		}
		require.False(t, ok, "packet type %d", tag.PacketType)
	}

	// the sequence start with the "fLaC" marker (E-RTMP v2)
	header := append([]byte("fLaC\x80\x00\x00\x22"), decodeHex(t, flacStreamInfo51)...)
	prober = NewProber(0)
	prober.HasAudio = true
	require.NoError(t, prober.PushTag(exAudioTag(flvio.FOURCC_FLAC, flvio.PKTTYPE_SEQUENCE_START, header), 0))
	pushCodedFrame(t, prober)
	require.True(t, prober.Probed())
	require.Len(t, prober.Streams, 1)
	require.Equal(t, stream.StreamInfoBytes(), prober.Streams[0].(flacparser.CodecData).StreamInfoBytes())
	require.Equal(t, header, prober.Streams[0].(flacparser.CodecData).SequenceHeaderBytes())

	// an invalid sequence start
	prober = NewProber(0)
	require.Error(t, prober.PushTag(exAudioTag(flvio.FOURCC_FLAC, flvio.PKTTYPE_SEQUENCE_START, []byte("fLaC")), 0))
}

func TestMuxDemuxFLAC(t *testing.T) {
	h264, err := h264parser.NewCodecDataFromAVCDecoderConfRecord(decodeHex(t, avcRecord320x240))
	require.NoError(t, err)
	// as FFmpeg writes the sequence start (the STREAMINFO alone), and a stream without one
	flac51, err := flacparser.NewCodecDataFromSequenceHeader(decodeHex(t, flacStreamInfo51))
	require.NoError(t, err)
	flacMono, err := flacparser.NewCodecDataFromStreamInfo(decodeHex(t, "040004000000000008150bb800f00000000000000000000000000000000000000000"))
	require.NoError(t, err)

	// FLAC as the default track and as track 1
	streams := []av.CodecData{h264, flac51, flacMono}
	packets := []av.Packet{
		{Idx: 0, IsKeyFrame: true, Data: []byte{0xA0}},
		{Idx: 1, IsKeyFrame: true, Data: decodeHex(t, "fff8aa5800e14c000000eb")},
		{Idx: 2, IsKeyFrame: true, Data: decodeHex(t, "fff8aa0800ed4c000000eb")},
		{Idx: 1, IsKeyFrame: true, Data: decodeHex(t, "fff8aa5801e64c0a7409bd"), Time: 21},
	}

	buf := &bytes.Buffer{}
	muxer := NewMuxer(buf)
	require.NoError(t, muxer.WriteHeader(streams))
	for _, pkt := range packets {
		require.NoError(t, muxer.WritePacket(pkt))
	}
	require.NoError(t, muxer.WriteTrailer())

	// the sequence start is written as it was received, or with the "fLaC" marker (E-RTMP v2)
	tag, ok, err := CodecDataToTag(flac51)
	require.NoError(t, err)
	require.True(t, ok)
	require.True(t, tag.IsExHeader)
	require.Equal(t, flvio.FOURCC_FLAC, tag.FourCC)
	require.Equal(t, decodeHex(t, flacStreamInfo51), tag.Data)
	tag, ok, err = CodecDataToTag(flacMono)
	require.NoError(t, err)
	require.True(t, ok)
	monoHeader := append([]byte("fLaC\x80\x00\x00\x22"), flacMono.StreamInfoBytes()...)
	require.Equal(t, monoHeader, tag.Data)
	flacMonoHeader, err := flacparser.NewCodecDataFromSequenceHeader(monoHeader)
	require.NoError(t, err)

	demuxer := NewDemuxer(bytes.NewReader(buf.Bytes()))
	got, err := demuxer.Streams()
	require.NoError(t, err)
	require.Equal(t, []av.CodecData{h264, flac51, flacMonoHeader}, got)
	for i, expected := range packets {
		pkt, err := demuxer.ReadPacket()
		require.NoError(t, err, "packet %d", i)
		require.Equal(t, expected, pkt, "packet %d", i)
	}

	metadata, err := NewMetadataByStreams(streams)
	require.NoError(t, err)
	require.Equal(t, flvio.FourCCToFloat(flvio.FOURCC_FLAC), metadata["audiocodecid"])
	require.Equal(t, 48000, metadata["audiosamplerate"])
	require.Equal(t, flvio.AMFMap{"1": flvio.AMFMap{"audiocodecid": flvio.FourCCToFloat(flvio.FOURCC_FLAC), "samplerate": 48000, "channels": 1}}, metadata["audioTrackIdInfoMap"])
}

// the start of AC-3 and E-AC-3 syncframes from FFmpeg's encoders: 48 kHz stereo and 5.1
const (
	ac3FrameStereo  = "0b7761e4144043e106f4"
	ac3Frame51      = "0b772b9a1e40ebf8403e"
	eac3FrameStereo = "0b77017f3487c0002084"
)

func TestProberAC3(t *testing.T) {
	for _, tc := range []struct {
		fourCC   [4]byte
		frame    string
		typ      av.CodecType
		channels int
	}{
		{flvio.FOURCC_AC3, ac3Frame51, av.AC3, 6},
		{flvio.FOURCC_EAC3, eac3FrameStereo, av.EAC3, 2},
	} {
		name := string(tc.fourCC[:])
		frame := decodeHex(t, tc.frame)

		// as FFmpeg writes AC-3 and E-AC-3: an empty sequence start, the multichannel config and the frames
		prober := NewProber(0)
		prober.PushMetadata(flvio.AMFMap{"audiocodecid": flvio.FourCCToFloat(tc.fourCC)})
		require.True(t, prober.HasAudio, name)
		require.NoError(t, prober.PushTag(exAudioTag(tc.fourCC, flvio.PKTTYPE_SEQUENCE_START, nil), 0), name)
		require.NoError(t, prober.PushTag(exAudioTag(tc.fourCC, flvio.AUDIO_PKTTYPE_MULTICHANNEL_CONFIG, []byte{0x01, byte(tc.channels), 0x00, 0x00, 0x00, 0x3f}), 0), name)
		require.False(t, prober.GotAudio, name)
		require.False(t, prober.Probed(), name)
		require.NoError(t, prober.PushTag(exAudioTag(tc.fourCC, flvio.PKTTYPE_CODED_FRAMES, frame), 32), name)
		require.True(t, prober.Probed(), name)
		require.Len(t, prober.Streams, 1, name)
		stream := prober.Streams[0].(ac3parser.CodecData)
		require.Equal(t, tc.typ, stream.Type(), name)
		require.Equal(t, 48000, stream.SampleRate(), name)
		require.Equal(t, tc.channels, stream.ChannelLayout().Count(), name)
		require.Equal(t, av.Packet{Data: frame, IsKeyFrame: true, Time: 32}, prober.PopPacket(), name)
		require.True(t, prober.Empty(), name)
	}

	// the frame doesn't match the FourCC
	prober := NewProber(0)
	require.Error(t, prober.PushTag(exAudioTag(flvio.FOURCC_AC3, flvio.PKTTYPE_CODED_FRAMES, decodeHex(t, eac3FrameStereo)), 0))
	prober = NewProber(0)
	require.Error(t, prober.PushTag(exAudioTag(flvio.FOURCC_EAC3, flvio.PKTTYPE_CODED_FRAMES, decodeHex(t, ac3FrameStereo)), 0))
	// no syncframe
	prober = NewProber(0)
	require.Error(t, prober.PushTag(exAudioTag(flvio.FOURCC_AC3, flvio.PKTTYPE_CODED_FRAMES, []byte{0x00, 0x01, 0x02}), 0))
}

func TestProberPendingTrack(t *testing.T) {
	// as FFmpeg writes a multitrack stream without TrackIdInfoMaps: the sequence starts of all tracks, then the
	// frames. The E-AC-3 and Opus tracks (without an ID header) only get their stream with their first frame,
	// which comes after the first frame of the default tracks.
	eac3 := decodeHex(t, eac3FrameStereo)
	for _, tc := range []struct {
		fourCC [4]byte
		frame  []byte
		typ    av.CodecType
	}{
		{flvio.FOURCC_EAC3, eac3, av.EAC3},
		{flvio.FOURCC_OPUS, []byte{0xfc, 0x01}, av.OPUS},
		{flvio.FOURCC_MP3, decodeHex(t, mp3FrameStereo48k), av.MP3},
	} {
		name := string(tc.fourCC[:])
		prober := NewProber(0)
		prober.PushMetadata(flvio.AMFMap{"audiocodecid": float64(flvio.SOUND_AAC)})
		require.NoError(t, prober.PushTag(aacTag(flvio.AAC_SEQHDR, []byte{0x12, 0x10}), 0), name)
		require.NoError(t, prober.PushTag(trackTag(exAudioTag(tc.fourCC, flvio.PKTTYPE_SEQUENCE_START, nil), 1), 0), name)
		require.NoError(t, prober.PushTag(aacTag(flvio.AAC_RAW, []byte{0xA0}), 0), name)
		require.False(t, prober.Probed(), name)
		require.NoError(t, prober.PushTag(trackTag(exAudioTag(tc.fourCC, flvio.PKTTYPE_CODED_FRAMES, tc.frame), 1), 0), name)
		require.True(t, prober.Probed(), name)
		require.Len(t, prober.Streams, 2, name)
		require.Equal(t, av.AAC, prober.Streams[0].Type(), name)
		require.Equal(t, tc.typ, prober.Streams[1].Type(), name)
	}

	// a pending track that never gets a frame ends probing after MaxProbePacketCount
	prober := NewProber(0)
	prober.MaxProbePacketCount = 4
	prober.PushMetadata(flvio.AMFMap{"audiocodecid": float64(flvio.SOUND_AAC)})
	require.NoError(t, prober.PushTag(aacTag(flvio.AAC_SEQHDR, []byte{0x12, 0x10}), 0))
	require.NoError(t, prober.PushTag(trackTag(exAudioTag(flvio.FOURCC_EAC3, flvio.PKTTYPE_SEQUENCE_START, nil), 1), 0))
	require.NoError(t, prober.PushTag(aacTag(flvio.AAC_RAW, []byte{0xA0}), 0))
	require.False(t, prober.Probed())
	require.NoError(t, prober.PushTag(aacTag(flvio.AAC_RAW, []byte{0xA1}), 0))
	require.True(t, prober.Probed())
	require.Len(t, prober.Streams, 1)
}

func TestMuxDemuxAC3(t *testing.T) {
	h264, err := h264parser.NewCodecDataFromAVCDecoderConfRecord(decodeHex(t, avcRecord320x240))
	require.NoError(t, err)
	ac3, err := ac3parser.NewCodecDataFromFrame(decodeHex(t, ac3Frame51))
	require.NoError(t, err)
	eac3, err := ac3parser.NewCodecDataFromFrame(decodeHex(t, eac3FrameStereo))
	require.NoError(t, err)

	// AC-3 as the default track and E-AC-3 as track 1
	streams := []av.CodecData{h264, ac3, eac3}
	packets := []av.Packet{
		{Idx: 0, IsKeyFrame: true, Data: []byte{0xA0}},
		{Idx: 1, IsKeyFrame: true, Data: decodeHex(t, ac3Frame51)},
		{Idx: 2, IsKeyFrame: true, Data: decodeHex(t, eac3FrameStereo)},
		{Idx: 1, IsKeyFrame: true, Data: decodeHex(t, ac3Frame51), Time: 32},
	}

	buf := &bytes.Buffer{}
	muxer := NewMuxer(buf)
	require.NoError(t, muxer.WriteHeader(streams))
	for _, pkt := range packets {
		require.NoError(t, muxer.WritePacket(pkt))
	}
	require.NoError(t, muxer.WriteTrailer())

	// an empty sequence start, as FFmpeg writes it
	tag, ok, err := CodecDataToTag(eac3)
	require.NoError(t, err)
	require.True(t, ok)
	require.True(t, tag.IsExHeader)
	require.Equal(t, flvio.FOURCC_EAC3, tag.FourCC)
	require.Equal(t, uint8(flvio.PKTTYPE_SEQUENCE_START), tag.PacketType)
	require.Empty(t, tag.Data)

	demuxer := NewDemuxer(bytes.NewReader(buf.Bytes()))
	got, err := demuxer.Streams()
	require.NoError(t, err)
	require.Equal(t, streams, got)
	for i, expected := range packets {
		pkt, err := demuxer.ReadPacket()
		require.NoError(t, err, "packet %d", i)
		require.Equal(t, expected, pkt, "packet %d", i)
	}

	metadata, err := NewMetadataByStreams(streams)
	require.NoError(t, err)
	require.Equal(t, flvio.FourCCToFloat(flvio.FOURCC_AC3), metadata["audiocodecid"])
	require.Equal(t, 48000, metadata["audiosamplerate"])
	require.Equal(t, flvio.AMFMap{"1": flvio.AMFMap{"audiocodecid": flvio.FourCCToFloat(flvio.FOURCC_EAC3), "samplerate": 48000, "channels": 2}}, metadata["audioTrackIdInfoMap"])
}

// the start of MP3 frames from FFmpeg (libmp3lame): MPEG-1 48 kHz stereo and MPEG-2 22.05 kHz mono
const (
	mp3FrameStereo48k = "fffb9464000001ee0850"
	mp3FrameMono22k   = "fff340c400133062843f"
)

func TestProberMP3(t *testing.T) {
	frame := decodeHex(t, mp3FrameStereo48k)

	// as FFmpeg writes MP3 at 48 kHz: the legacy header with 44 kHz, the sample rate is in the frame header
	for _, soundFormat := range []uint8{flvio.SOUND_MP3, flvio.SOUND_MP3_8KHZ} {
		prober := NewProber(0)
		prober.PushMetadata(flvio.AMFMap{"audiocodecid": float64(soundFormat)})
		require.True(t, prober.HasAudio)
		tag := flvio.Tag{Type: flvio.TAG_AUDIO, SoundFormat: soundFormat, SoundRate: flvio.SOUND_44Khz, SoundSize: flvio.SOUND_16BIT, SoundType: flvio.SOUND_STEREO, Data: frame}
		require.NoError(t, prober.PushTag(tag, 24))
		require.True(t, prober.Probed())
		require.Len(t, prober.Streams, 1)
		stream := prober.Streams[0].(mp3parser.CodecData)
		require.Equal(t, av.MP3, stream.Type())
		require.Equal(t, 48000, stream.SampleRate())
		require.Equal(t, av.CH_STEREO, stream.ChannelLayout())
		require.Equal(t, av.Packet{Data: frame, IsKeyFrame: true, Time: 24}, prober.PopPacket())

		// an empty frame is dropped
		tag.Data = nil
		_, ok := prober.TagToPacket(tag, 48)
		require.False(t, ok)
	}

	// the ExAudioTagHeader
	prober := NewProber(0)
	prober.HasAudio = true
	require.NoError(t, prober.PushTag(exAudioTag(flvio.FOURCC_MP3, flvio.PKTTYPE_SEQUENCE_START, nil), 0))
	require.False(t, prober.Probed())
	require.NoError(t, prober.PushTag(exAudioTag(flvio.FOURCC_MP3, flvio.PKTTYPE_CODED_FRAMES, decodeHex(t, mp3FrameMono22k)), 0))
	require.True(t, prober.Probed())
	require.Equal(t, 22050, prober.Streams[0].(av.AudioCodecData).SampleRate())
	require.Equal(t, av.CH_MONO, prober.Streams[0].(av.AudioCodecData).ChannelLayout())

	// no frame header
	prober = NewProber(0)
	require.Error(t, prober.PushTag(flvio.Tag{Type: flvio.TAG_AUDIO, SoundFormat: flvio.SOUND_MP3, Data: []byte{0x00, 0x01, 0x02, 0x03}}, 0))
}

func TestMuxDemuxMP3(t *testing.T) {
	h264, err := h264parser.NewCodecDataFromAVCDecoderConfRecord(decodeHex(t, avcRecord320x240))
	require.NoError(t, err)
	stereo48k, err := mp3parser.NewCodecDataFromFrame(decodeHex(t, mp3FrameStereo48k))
	require.NoError(t, err)
	mono22k, err := mp3parser.NewCodecDataFromFrame(decodeHex(t, mp3FrameMono22k))
	require.NoError(t, err)

	// MP3 as the default track (legacy header) and as track 1 (ExAudioTagHeader)
	streams := []av.CodecData{h264, stereo48k, mono22k}
	packets := []av.Packet{
		{Idx: 0, IsKeyFrame: true, Data: []byte{0xA0}},
		{Idx: 1, IsKeyFrame: true, Data: decodeHex(t, mp3FrameStereo48k)},
		{Idx: 2, IsKeyFrame: true, Data: decodeHex(t, mp3FrameMono22k)},
		{Idx: 1, IsKeyFrame: true, Data: decodeHex(t, mp3FrameStereo48k), Time: 24},
	}

	buf := &bytes.Buffer{}
	muxer := NewMuxer(buf)
	require.NoError(t, muxer.WriteHeader(streams))
	for _, pkt := range packets {
		require.NoError(t, muxer.WritePacket(pkt))
	}
	require.NoError(t, muxer.WriteTrailer())

	demuxer := NewDemuxer(bytes.NewReader(buf.Bytes()))
	got, err := demuxer.Streams()
	require.NoError(t, err)
	require.Equal(t, streams, got)
	for i, expected := range packets {
		pkt, err := demuxer.ReadPacket()
		require.NoError(t, err, "packet %d", i)
		require.Equal(t, expected, pkt, "packet %d", i)
	}

	// the default track has no sequence start, a track from 1 on an empty one (ExAudioTagHeader)
	tags, err := TrackCodecDataTags(stereo48k, 0)
	require.NoError(t, err)
	require.Empty(t, tags)
	tags, err = TrackCodecDataTags(mono22k, 1)
	require.NoError(t, err)
	require.Len(t, tags, 2)
	tag := tags[0]
	require.True(t, tag.IsExHeader)
	require.True(t, tag.IsMultitrack)
	require.Equal(t, uint8(1), tag.TrackID)
	require.Equal(t, flvio.FOURCC_MP3, tag.FourCC)
	require.Equal(t, uint8(flvio.PKTTYPE_SEQUENCE_START), tag.PacketType)
	require.Empty(t, tag.Data)

	// the frames of the default track have the legacy header, 48 kHz as 44 kHz
	tag, _ = PacketToTag(packets[1], stereo48k)
	require.False(t, tag.IsExHeader)
	require.Equal(t, uint8(flvio.SOUND_MP3), tag.SoundFormat)
	require.Equal(t, uint8(flvio.SOUND_44Khz), tag.SoundRate)
	require.Equal(t, uint8(flvio.SOUND_STEREO), tag.SoundType)
	tag, _ = PacketToTag(packets[2], mono22k)
	require.Equal(t, uint8(flvio.SOUND_22Khz), tag.SoundRate)
	require.Equal(t, uint8(flvio.SOUND_MONO), tag.SoundType)
	// as a track from 1 on, it is converted to the ExAudioTagHeader
	tag = TrackTag(tag, 1)
	require.True(t, tag.IsExHeader)
	require.Equal(t, flvio.FOURCC_MP3, tag.FourCC)
	require.Equal(t, uint8(flvio.PKTTYPE_CODED_FRAMES), tag.PacketType)

	// 8 kHz has its own SoundFormat
	mono8k, err := mp3parser.NewCodecDataFromFrame([]byte{0xff, 0xe3, 0x28, 0xc4})
	require.NoError(t, err)
	require.Equal(t, uint8(flvio.SOUND_MP3_8KHZ), mp3Tag(mono8k).SoundFormat)

	metadata, err := NewMetadataByStreams(streams)
	require.NoError(t, err)
	require.Equal(t, flvio.SOUND_MP3, metadata["audiocodecid"])
	require.Equal(t, 48000, metadata["audiosamplerate"])
	require.Equal(t, flvio.AMFMap{"1": flvio.AMFMap{"audiocodecid": flvio.FourCCToFloat(flvio.FOURCC_MP3), "samplerate": 22050, "channels": 1}}, metadata["audioTrackIdInfoMap"])
	metadata, err = NewMetadataByStreams([]av.CodecData{mono8k})
	require.NoError(t, err)
	require.Equal(t, flvio.SOUND_MP3_8KHZ, metadata["audiocodecid"])
}

func TestTrackCodecDataTags(t *testing.T) {
	// the tags at the start of the audio tracks of an FFmpeg FLV with AAC, AC-3, FLAC and MP3 (44.1 kHz mono):
	// AAC as the default track has the legacy header, the other tracks have a sequence start and a
	// multichannel config (mono)
	aac, err := aacparser.NewCodecDataFromMPEG4AudioConfigBytes(decodeHex(t, "118856e500"))
	require.NoError(t, err)
	ac3, err := ac3parser.NewCodecDataFromFrame(decodeHex(t, "0b775f1d0c402f84"))
	require.NoError(t, err)
	flac, err := flacparser.NewCodecDataFromSequenceHeader(decodeHex(t, "040004000000000008150bb800f00000000000000000000000000000000000000000"))
	require.NoError(t, err)
	mp3, err := mp3parser.NewCodecDataFromFrame(decodeHex(t, "fffb50c4"))
	require.NoError(t, err)

	streams := []av.CodecData{aac, ac3, flac, mp3}
	expected := [][]string{
		{"af00118856e500"},
		{"950061632d3301", "950461632d3301010100000004"},
		{"9500664c614302040004000000000008150bb800f00000000000000000000000000000000000000000", "9504664c614302010100000004"},
		{"95002e6d703303", "95042e6d703303010100000004"},
	}
	trackIDs, err := TrackIDs(streams)
	require.NoError(t, err)
	for i, stream := range streams {
		tags, err := TrackCodecDataTags(stream, trackIDs[i])
		require.NoError(t, err)
		require.Len(t, tags, len(expected[i]), "%v", stream.Type())
		for j, tag := range tags {
			buf := &bytes.Buffer{}
			require.NoError(t, flvio.WriteTag(buf, tag, 0, make([]byte, 256)))
			body := buf.Bytes()[flvio.TagHeaderLength : buf.Len()-4]
			require.Equal(t, expected[i][j], hex.EncodeToString(body), "%v tag %d", stream.Type(), j)

			// it is read back as it was written
			read, _, err := flvio.ReadTag(bytes.NewReader(buf.Bytes()), make([]byte, 256))
			require.NoError(t, err)
			require.Equal(t, tag.PacketType, read.PacketType)
			require.Equal(t, tag.TrackID, read.TrackID)
		}
	}

	// the channel flags of the multichannel config
	opus51, err := opusparser.NewCodecDataFromOpusHead(decodeHex(t, opusHead51))
	require.NoError(t, err)
	tags, err := TrackCodecDataTags(opus51, 0)
	require.NoError(t, err)
	require.Len(t, tags, 2)
	require.False(t, tags[1].IsMultitrack)
	require.Equal(t, uint8(flvio.AUDIO_PKTTYPE_MULTICHANNEL_CONFIG), tags[1].PacketType)
	require.Equal(t, flvio.FOURCC_OPUS, tags[1].FourCC)
	// FL, FR, FC, LFE, BL, BR, as FFmpeg writes 5.1
	require.Equal(t, []byte{0x01, 0x06, 0x00, 0x00, 0x00, 0x3f}, tags[1].Data)

	for _, tc := range []struct {
		layout av.ChannelLayout
		flags  uint32
	}{
		{av.CH_MONO, 0x04},
		{av.CH_STEREO, 0x03},
		{av.CH_2_1, 0x103},
		{av.CH_SURROUND | av.CH_SIDE_LEFT | av.CH_SIDE_RIGHT | av.CH_LOW_FREQ, 0x60f},
	} {
		require.Equal(t, tc.flags, channelFlags(tc.layout), "%v", tc.layout)
	}

	// video has no multichannel config
	h264, err := h264parser.NewCodecDataFromAVCDecoderConfRecord(decodeHex(t, avcRecord320x240))
	require.NoError(t, err)
	tags, err = TrackCodecDataTags(h264, 1)
	require.NoError(t, err)
	require.Len(t, tags, 1)
}

// readTags returns the tags of an FLV file.
func readTags(t *testing.T, b []byte) []flvio.Tag {
	r := bytes.NewReader(b)
	_, err := r.Seek(flvio.FileHeaderLength+4, io.SeekStart)
	require.NoError(t, err)
	tags := []flvio.Tag{}
	for {
		tag, _, err := flvio.ReadTag(r, make([]byte, 256))
		if err == io.EOF {
			return tags
		}
		require.NoError(t, err)
		tags = append(tags, tag)
	}
}

func TestMuxerMetadata(t *testing.T) {
	h264, err := h264parser.NewCodecDataFromAVCDecoderConfRecord(decodeHex(t, avcRecord320x240))
	require.NoError(t, err)
	aac, err := aacparser.NewCodecDataFromMPEG4AudioConfigBytes([]byte{0x12, 0x10})
	require.NoError(t, err)
	streams := []av.CodecData{h264, h264, aac}

	buf := &bytes.Buffer{}
	muxer := NewMuxer(buf)
	require.NoError(t, muxer.WriteHeader(streams))
	require.NoError(t, muxer.WritePacket(av.Packet{Idx: 0, IsKeyFrame: true, Data: []byte{0xA0}}))
	require.NoError(t, muxer.WriteTrailer())

	// the metadata is the first tag, without duration and file size (not seekable)
	tags := readTags(t, buf.Bytes())
	require.Equal(t, uint8(flvio.TAG_SCRIPTDATA), tags[0].Type)
	metadata := ParseMetadata(tags[0].Data)
	require.NotNil(t, metadata)
	require.Equal(t, float64(flvio.VIDEO_H264), metadata["videocodecid"])
	require.Equal(t, 320.0, metadata["width"])
	require.Equal(t, float64(flvio.SOUND_AAC), metadata["audiocodecid"])
	require.Equal(t, flvio.AMFMap{"1": flvio.AMFMap{"videocodecid": flvio.FourCCToFloat(flvio.FOURCC_AVC), "width": 320.0, "height": 240.0}}, metadata["videoTrackIdInfoMap"])
	require.NotContains(t, metadata, "duration")
	require.NotContains(t, metadata, "filesize")
	require.Equal(t, uint8(flvio.TAG_VIDEO), tags[1].Type)
}

func TestMuxerMetadataSeekable(t *testing.T) {
	h264, err := h264parser.NewCodecDataFromAVCDecoderConfRecord(decodeHex(t, avcRecord320x240))
	require.NoError(t, err)

	file, err := os.Create(t.TempDir() + "/test.flv")
	require.NoError(t, err)
	defer file.Close()
	// the file doesn't start at the beginning of the writer
	_, err = file.Write([]byte("prefix"))
	require.NoError(t, err)

	muxer := NewMuxer(file)
	require.NoError(t, muxer.WriteHeader([]av.CodecData{h264}))
	for i := 0; i < 10; i++ {
		require.NoError(t, muxer.WritePacket(av.Packet{Idx: 0, IsKeyFrame: i == 0, Data: []byte{0xA0}, Time: int64(i) * 40}))
	}
	require.NoError(t, muxer.WriteTrailer())

	b, err := os.ReadFile(file.Name())
	require.NoError(t, err)
	require.Equal(t, "prefix", string(b[:6]))
	b = b[6:]
	metadata := ParseMetadata(readTags(t, b)[0].Data)
	require.Equal(t, 0.36, metadata["duration"])
	require.Equal(t, float64(len(b)), metadata["filesize"])

	// the file is read back
	demuxer := NewDemuxer(bytes.NewReader(b))
	streams, err := demuxer.Streams()
	require.NoError(t, err)
	require.Equal(t, []av.CodecData{h264}, streams)

	// a writer that can't seek (e.g. a pipe) gets no duration and file size
	buf := &bytes.Buffer{}
	muxer = NewMuxer(failingSeeker{buf})
	require.NoError(t, muxer.WriteHeader([]av.CodecData{h264}))
	require.NoError(t, muxer.WriteTrailer())
	metadata = ParseMetadata(readTags(t, buf.Bytes())[0].Data)
	require.NotContains(t, metadata, "duration")
}

type failingSeeker struct {
	io.Writer
}

func (failingSeeker) Seek(offset int64, whence int) (int64, error) {
	return 0, errors.New("not seekable")
}

// writeFLV returns an FLV file with the file header flags and the tags.
func writeFLV(t *testing.T, flags uint8, tags ...flvio.Tag) []byte {
	buf := &bytes.Buffer{}
	b := make([]byte, 256)
	n := flvio.FillFileHeader(b, flags)
	buf.Write(b[:n])
	for _, tag := range tags {
		require.NoError(t, flvio.WriteTag(buf, tag, 0, b))
	}
	return buf.Bytes()
}

func TestDemuxerMetadata(t *testing.T) {
	record := decodeHex(t, avcRecord320x240)
	h264, err := h264parser.NewCodecDataFromAVCDecoderConfRecord(record)
	require.NoError(t, err)
	seqhdr := flvio.Tag{Type: flvio.TAG_VIDEO, CodecID: flvio.VIDEO_H264, FrameType: flvio.FRAME_KEY, AVCPacketType: flvio.AVC_SEQHDR, Data: record}
	frame := flvio.Tag{Type: flvio.TAG_VIDEO, CodecID: flvio.VIDEO_H264, FrameType: flvio.FRAME_KEY, AVCPacketType: flvio.AVC_NALU, Data: []byte{0xA0}}

	// the file header has audio, but the metadata has an unsupported audio codec (ADPCM): probing doesn't wait
	// for it
	b := writeFLV(t, flvio.FILE_HAS_VIDEO|flvio.FILE_HAS_AUDIO,
		MetadataToTag(flvio.AMFMap{"videocodecid": float64(flvio.VIDEO_H264), "audiocodecid": 1.0}),
		seqhdr, frame,
	)
	streams, err := NewDemuxer(bytes.NewReader(b)).Streams()
	require.NoError(t, err)
	require.Equal(t, []av.CodecData{h264}, streams)

	// the second video track is in the metadata: probing waits for its sequence start, which comes after the
	// first frame
	metadata, err := NewMetadataByStreams([]av.CodecData{h264, h264})
	require.NoError(t, err)
	b = writeFLV(t, flvio.FILE_HAS_VIDEO,
		MetadataToTag(metadata),
		seqhdr, frame,
		TrackTag(seqhdr, 1), TrackTag(frame, 1),
	)
	demuxer := NewDemuxer(bytes.NewReader(b))
	streams, err = demuxer.Streams()
	require.NoError(t, err)
	require.Equal(t, []av.CodecData{h264, h264}, streams)
	for _, idx := range []int8{0, 1} {
		pkt, err := demuxer.ReadPacket()
		require.NoError(t, err)
		require.Equal(t, idx, pkt.Idx)
	}
}

func TestParseMetadata(t *testing.T) {
	data := make([]byte, 256)
	n := flvio.FillAMF0Val(data, "@setDataFrame")
	n += flvio.FillAMF0Val(data[n:], "onMetaData")
	n += flvio.FillAMF0Val(data[n:], flvio.AMFMap{"width": 640})
	require.Equal(t, flvio.AMFMap{"width": 640.0}, ParseMetadata(data[:n]))

	require.Nil(t, ParseMetadata(nil))
	require.Nil(t, ParseMetadata([]byte{0xff, 0x00}))
	n = flvio.FillAMF0Val(data, "onMetaData")
	require.Nil(t, ParseMetadata(data[:n]))
	n += flvio.FillAMF0Val(data[n:], "no map")
	require.Nil(t, ParseMetadata(data[:n]))
}
