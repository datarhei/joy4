// Package pktque provides packet Filter interface and structures used by other components.
package pktque

import (
	"time"

	"github.com/datarhei/joy4/av"
)

type Filter interface {
	// Change packet time or drop packet
	ModifyPacket(pkt *av.Packet, streams []av.CodecData, videoidx int, audioidx int) (drop bool, err error)
}

// Combine multiple Filters into one, ModifyPacket will be called in order.
type Filters []Filter

func (self Filters) ModifyPacket(pkt *av.Packet, streams []av.CodecData, videoidx int, audioidx int) (drop bool, err error) {
	for _, filter := range self {
		if drop, err = filter.ModifyPacket(pkt, streams, videoidx, audioidx); err != nil {
			return
		}
		if drop {
			return
		}
	}
	return
}

// Wrap origin Demuxer and Filter into a new Demuxer, when read this Demuxer filters will be called.
type FilterDemuxer struct {
	av.Demuxer
	Filter   Filter
	streams  []av.CodecData
	videoidx int
	audioidx int
}

func (self FilterDemuxer) ReadPacket() (pkt av.Packet, err error) {
	if self.streams == nil {
		if self.streams, err = self.Demuxer.Streams(); err != nil {
			return
		}
		// the first video and audio stream, e.g. the default tracks of a multitrack stream (E-RTMP v2)
		videoFound, audioFound := false, false
		for i, stream := range self.streams {
			if stream.Type().IsVideo() && !videoFound {
				self.videoidx = i
				videoFound = true
			} else if stream.Type().IsAudio() && !audioFound {
				self.audioidx = i
				audioFound = true
			}
		}
	}

	for {
		if pkt, err = self.Demuxer.ReadPacket(); err != nil {
			return
		}
		var drop bool
		if drop, err = self.Filter.ModifyPacket(&pkt, self.streams, self.videoidx, self.audioidx); err != nil {
			return
		}
		if !drop {
			break
		}
	}

	return
}

// Drop packets until first video key frame arrived.
// WaitKeyFrame drops the packets before the first key frame of the first video stream. After it, the packets
// of each other video stream (e.g. the tracks of an E-RTMP v2 multitrack stream) are dropped until the first
// key frame of that stream, because their key frames are not necessarily at the same time.
type WaitKeyFrame struct {
	ok bool
	// the video streams that had their first key frame
	started map[int8]bool
}

func (self *WaitKeyFrame) ModifyPacket(pkt *av.Packet, streams []av.CodecData, videoidx int, audioidx int) (drop bool, err error) {
	if !self.ok {
		if pkt.Idx != int8(videoidx) || !pkt.IsKeyFrame {
			drop = true
			return
		}
		self.ok = true
	}

	if pkt.Idx >= 0 && int(pkt.Idx) < len(streams) && streams[pkt.Idx].Type().IsVideo() && !self.started[pkt.Idx] {
		if !pkt.IsKeyFrame {
			drop = true
			return
		}
		if self.started == nil {
			self.started = map[int8]bool{}
		}
		self.started[pkt.Idx] = true
	}

	return
}

// Fix incorrect packet timestamps.
type FixTime struct {
	zeroSet       bool
	zerobase      int64
	incrbase      int64
	lasttime      int64
	StartFromZero bool // make timestamp start from zero
	MakeIncrement bool // force timestamp increment
}

func (self *FixTime) ModifyPacket(pkt *av.Packet, streams []av.CodecData, videoidx int, audioidx int) (drop bool, err error) {
	if self.StartFromZero {
		if !self.zeroSet {
			self.zerobase = pkt.Time
			self.zeroSet = true
		}
		pkt.Time -= self.zerobase
	}

	if self.MakeIncrement {
		pkt.Time -= self.incrbase
		if self.lasttime == 0 {
			self.lasttime = pkt.Time
		}
		if pkt.Time < self.lasttime || pkt.Time > self.lasttime+500 {
			self.incrbase += pkt.Time - self.lasttime
			pkt.Time = self.lasttime
		}
		self.lasttime = pkt.Time
	}

	return
}

// Drop incorrect packets to make A/V sync.
type AVSync struct {
	MaxTimeDiff int64
	time        []int64
}

func (self *AVSync) ModifyPacket(pkt *av.Packet, streams []av.CodecData, videoidx int, audioidx int) (drop bool, err error) {
	if self.time == nil {
		self.time = make([]int64, len(streams))
		if self.MaxTimeDiff == 0 {
			self.MaxTimeDiff = 500
		}
	}

	start, end, correctable, correcttime := self.check(int(pkt.Idx))
	if pkt.Time >= start && pkt.Time < end {
		self.time[pkt.Idx] = pkt.Time
	} else {
		if correctable {
			pkt.Time = correcttime
			for i := range self.time {
				self.time[i] = correcttime
			}
		} else {
			drop = true
		}
	}
	return
}

func (self *AVSync) check(i int) (start int64, end int64, correctable bool, correcttime int64) {
	minidx := -1
	maxidx := -1
	for j := range self.time {
		if minidx == -1 || self.time[j] < self.time[minidx] {
			minidx = j
		}
		if maxidx == -1 || self.time[j] > self.time[maxidx] {
			maxidx = j
		}
	}
	allthesame := self.time[minidx] == self.time[maxidx]

	if i == maxidx {
		if allthesame {
			correctable = true
		} else {
			correctable = false
		}
	} else {
		correctable = true
	}

	start = self.time[minidx]
	end = start + self.MaxTimeDiff
	correcttime = start + 40
	return
}

// Make packets reading speed as same as walltime, effect like ffmpeg -re option.
type Walltime struct {
	started   bool
	start     time.Time
	firsttime int64
}

func (self *Walltime) ModifyPacket(pkt *av.Packet, streams []av.CodecData, videoidx int, audioidx int) (drop bool, err error) {
	if !self.started {
		self.started = true
		self.start = time.Now()
		self.firsttime = pkt.Time
	}
	delta := time.Duration(pkt.Time-self.firsttime)*time.Millisecond - time.Since(self.start)
	if delta > 0 {
		time.Sleep(delta)
	}
	return
}
