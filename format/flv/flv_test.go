package flv

import (
	"bytes"
	"testing"
	"time"

	"github.com/datarhei/joy4/av"
	"github.com/datarhei/joy4/codec"
	"github.com/datarhei/joy4/codec/aacparser"
	"github.com/datarhei/joy4/codec/fake"
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
		// unsupported: H.263, VP6, MP3, ADPCM, Opus FourCC as a string
		{flvio.AMFMap{"videocodecid": 2.0, "audiocodecid": 2.0}, false, false},
		{flvio.AMFMap{"videocodecid": 4.0, "audiocodecid": 1.0}, false, false},
		{flvio.AMFMap{"videocodecid": "VP62", "audiocodecid": ".mp3"}, false, false},
		{flvio.AMFMap{"audiocodecid": "Opus"}, false, false},
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
