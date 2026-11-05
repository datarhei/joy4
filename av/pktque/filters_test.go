package pktque

import (
	"io"
	"testing"
	"time"

	"github.com/datarhei/joy4/av"
	"github.com/datarhei/joy4/codec/fake"
	"github.com/stretchr/testify/require"
)

func TestWalltime(t *testing.T) {
	filter := &Walltime{}

	start := time.Now()

	// the first packet sets the start, no matter what its time is
	pkt := av.Packet{Idx: 1, Time: 10000}
	drop, err := filter.ModifyPacket(&pkt, nil, 0, 1)
	require.NoError(t, err)
	require.False(t, drop)
	require.Less(t, time.Since(start), 100*time.Millisecond)

	// packets of all streams are paced relative to the first packet
	for _, p := range []av.Packet{{Idx: 0, Time: 10100}, {Idx: 1, Time: 10200}, {Idx: 0, Time: 10300}} {
		drop, err = filter.ModifyPacket(&p, nil, 0, 1)
		require.NoError(t, err)
		require.False(t, drop)
		require.GreaterOrEqual(t, time.Since(start), time.Duration(p.Time-10000)*time.Millisecond)
	}
	require.Less(t, time.Since(start), 600*time.Millisecond)

	// a packet in the past is not delayed
	before := time.Now()
	pkt = av.Packet{Idx: 0, Time: 10000}
	_, err = filter.ModifyPacket(&pkt, nil, 0, 1)
	require.NoError(t, err)
	require.Less(t, time.Since(before), 50*time.Millisecond)
}

type testDemuxer struct {
	streams []av.CodecData
	packets []av.Packet
}

func (d *testDemuxer) Streams() ([]av.CodecData, error) { return d.streams, nil }

func (d *testDemuxer) ReadPacket() (av.Packet, error) {
	if len(d.packets) == 0 {
		return av.Packet{}, io.EOF
	}
	pkt := d.packets[0]
	d.packets = d.packets[1:]
	return pkt, nil
}

func TestFilterDemuxerFirstVideoStream(t *testing.T) {
	// two video tracks, WaitKeyFrame waits for a key frame of the first one
	demuxer := FilterDemuxer{
		Filter: &WaitKeyFrame{},
		Demuxer: &testDemuxer{
			streams: []av.CodecData{fake.CodecData{CodecType_: av.H264}, fake.CodecData{CodecType_: av.H264}, fake.CodecData{CodecType_: av.AAC}},
			packets: []av.Packet{
				{Idx: 1, IsKeyFrame: true, Time: 0},
				{Idx: 0, Time: 0},
				{Idx: 0, IsKeyFrame: true, Time: 40},
				{Idx: 1, Time: 40},
			},
		},
	}

	pkt, err := demuxer.ReadPacket()
	require.NoError(t, err)
	require.Equal(t, av.Packet{Idx: 0, IsKeyFrame: true, Time: 40}, pkt)
	pkt, err = demuxer.ReadPacket()
	require.NoError(t, err)
	require.Equal(t, av.Packet{Idx: 1, Time: 40}, pkt)
}
