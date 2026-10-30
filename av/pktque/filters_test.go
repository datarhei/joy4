package pktque

import (
	"testing"
	"time"

	"github.com/datarhei/joy4/av"
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
