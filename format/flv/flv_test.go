package flv

import (
	"bytes"
	"testing"

	"github.com/datarhei/joy4/av"
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
