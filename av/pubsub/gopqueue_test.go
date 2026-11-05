package pubsub

import (
	"testing"

	"github.com/datarhei/joy4/av"
	"github.com/datarhei/joy4/codec/fake"
	"github.com/stretchr/testify/require"
)

func TestQueueGopsOfFirstVideoStream(t *testing.T) {
	// two video tracks with different GOPs, the GOPs of the first one are kept
	q := NewQueue()
	require.NoError(t, q.WriteHeader([]av.CodecData{fake.CodecData{CodecType_: av.H264}, fake.CodecData{CodecType_: av.H264}}))
	for _, pkt := range []av.Packet{
		{Idx: 0, IsKeyFrame: true, Time: 0},
		{Idx: 1, IsKeyFrame: true, Time: 0},
		{Idx: 0, Time: 40},
		{Idx: 1, Time: 40},
		{Idx: 1, IsKeyFrame: true, Time: 60},
		{Idx: 0, Time: 80},
		{Idx: 1, Time: 80},
		{Idx: 1, IsKeyFrame: true, Time: 100},
	} {
		require.NoError(t, q.WritePacket(pkt))
	}

	pkt, err := q.Oldest().ReadPacket()
	require.NoError(t, err)
	require.Equal(t, av.Packet{Idx: 0, IsKeyFrame: true, Time: 0}, pkt)
}
