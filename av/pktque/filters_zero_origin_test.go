package pktque

import (
	"fmt"
	"testing"

	"github.com/datarhei/joy4/av"
)

func TestFixTimeStartFromZero(t *testing.T) {
	for _, base := range []int64{0, -1000, 1000, 49 * 24 * 60 * 60 * 1000} {
		for _, increment := range []bool{false, true} {
			t.Run(fmt.Sprintf("origin_%d/increment_%t", base, increment), func(t *testing.T) {
				filter := FixTime{StartFromZero: true, MakeIncrement: increment}
				// Video and audio share the first timestamp. A later audio
				// packet must not reset that origin when it happens to be zero.
				packets := []av.Packet{
					{Idx: 0, Time: base},
					{Idx: 1, Time: base},
					{Idx: 1, Time: base + 21},
					{Idx: 0, Time: base + 40},
					{Idx: 1, Time: base + 42},
				}
				for _, packet := range packets {
					want := packet.Time - base
					drop, err := filter.ModifyPacket(&packet, nil, 0, 1)
					if err != nil || drop {
						t.Fatalf("ModifyPacket: drop=%t, err=%v", drop, err)
					}
					if packet.Time != want {
						t.Fatalf("got timestamp %d, want %d", packet.Time, want)
					}
				}
			})
		}
	}
}

func TestFixTimeStartFromZeroDisabled(t *testing.T) {
	filter := FixTime{}
	for _, stamp := range []int64{-1000, 0, 0, 21, 40, 42} {
		packet := av.Packet{Time: stamp}
		drop, err := filter.ModifyPacket(&packet, nil, 0, 1)
		if err != nil || drop || packet.Time != stamp {
			t.Fatalf("timestamp %d: got %d, drop=%t, err=%v", stamp, packet.Time, drop, err)
		}
	}
}
