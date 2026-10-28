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
