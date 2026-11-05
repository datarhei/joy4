// Package ac3parser parses the header of AC-3 and E-AC-3 syncframes (ATSC A/52).
package ac3parser

import (
	"fmt"
	"time"

	"github.com/datarhei/joy4/av"
)

// BlockSize is the number of samples per channel of an audio block. An AC-3 syncframe has 6 blocks, an
// E-AC-3 syncframe 1, 2, 3 or 6.
const BlockSize = 256

// FrameHeader is the header of a syncframe: the syncinfo and the start of the bit stream information (BSI).
type FrameHeader struct {
	// whether it is an E-AC-3 syncframe (bsid 11 to 16)
	EAC3 bool
	Bsid int
	// E-AC-3: the stream type (0 independent, 1 dependent, 2 converted from AC-3) and the substream id
	StreamType  int
	SubstreamID int
	SampleRate  int
	// audio blocks per syncframe
	Blocks int
	// audio coding mode (acmod), the channels without the LFE channel
	ChannelMode int
	LFE         bool
}

// Samples returns the number of samples per channel of the syncframe.
func (h FrameHeader) Samples() int {
	return h.Blocks * BlockSize
}

// ChannelLayout returns the channel layout of the audio coding mode and the LFE channel.
func (h FrameHeader) ChannelLayout() av.ChannelLayout {
	layout := channelLayouts[h.ChannelMode]
	if h.LFE {
		layout |= av.CH_LOW_FREQ
	}
	return layout
}

// channelLayouts are the channel layouts of the audio coding modes (acmod). The mode 0 is two independent
// mono channels (1+1), which are decoded as stereo.
var channelLayouts = [8]av.ChannelLayout{
	av.CH_STEREO,
	av.CH_MONO,
	av.CH_STEREO,
	av.CH_SURROUND,
	av.CH_2_1,
	av.CH_SURROUND | av.CH_BACK_CENTER,
	av.CH_STEREO | av.CH_SIDE_LEFT | av.CH_SIDE_RIGHT,
	av.CH_SURROUND | av.CH_SIDE_LEFT | av.CH_SIDE_RIGHT,
}

var sampleRates = [3]int{48000, 44100, 32000}

// E-AC-3 with the reduced sample rates (fscod 3)
var reducedSampleRates = [3]int{24000, 22050, 16000}

var eac3Blocks = [4]int{1, 2, 3, 6}

// ParseFrameHeader parses the header of the syncframe at the start of data.
func ParseFrameHeader(data []byte) (h FrameHeader, err error) {
	if len(data) < 8 || data[0] != 0x0b || data[1] != 0x77 {
		err = fmt.Errorf("ac3parser: no syncframe")
		return
	}

	// bsid is at the same position in AC-3 and E-AC-3
	h.Bsid = int(data[5] >> 3)

	switch {
	case h.Bsid <= 10:
		// AC-3: syncword, crc1 (16), fscod (2), frmsizecod (6), bsid (5), bsmod (3), acmod (3), ...
		fscod := int(data[4] >> 6)
		if fscod == 3 {
			err = fmt.Errorf("ac3parser: reserved sample rate")
			return
		}
		// bsid 9 and 10 have half and quarter the sample rate
		shift := 0
		if h.Bsid > 8 {
			shift = h.Bsid - 8
		}
		h.SampleRate = sampleRates[fscod] >> shift
		h.Blocks = 6

		r := bitReader{data: data[6:]}
		h.ChannelMode = r.read(3)
		if h.ChannelMode&0x01 != 0 && h.ChannelMode != 1 {
			r.read(2) // cmixlev
		}
		if h.ChannelMode&0x04 != 0 {
			r.read(2) // surmixlev
		}
		if h.ChannelMode == 2 {
			r.read(2) // dsurmod
		}
		h.LFE = r.read(1) == 1

	case h.Bsid <= 16:
		// E-AC-3: syncword, strmtyp (2), substreamid (3), frmsiz (11), fscod (2), fscod2 or numblkscod (2),
		// acmod (3), lfeon (1), bsid (5), ...
		h.EAC3 = true
		h.StreamType = int(data[2] >> 6)
		h.SubstreamID = int(data[2] >> 3 & 0x07)
		if h.StreamType == 3 {
			err = fmt.Errorf("ac3parser: reserved stream type")
			return
		}
		fscod := int(data[4] >> 6)
		code := int(data[4] >> 4 & 0x03)
		if fscod == 3 {
			if code == 3 {
				err = fmt.Errorf("ac3parser: reserved sample rate")
				return
			}
			h.SampleRate = reducedSampleRates[code]
			h.Blocks = 6
		} else {
			h.SampleRate = sampleRates[fscod]
			h.Blocks = eac3Blocks[code]
		}
		h.ChannelMode = int(data[4] >> 1 & 0x07)
		h.LFE = data[4]&0x01 == 1

	default:
		err = fmt.Errorf("ac3parser: unsupported bsid %d", h.Bsid)
	}

	return
}

// CodecData is the codec data of an AC-3 or E-AC-3 stream, from its first syncframe.
type CodecData struct {
	CodecType_ av.CodecType
	FrameHeader
}

// NewCodecDataFromFrame returns the codec data of the syncframe at the start of data. An E-AC-3 stream must
// start with an independent substream. The channels of dependent substreams (more than 5.1 channels) are
// not included.
func NewCodecDataFromFrame(data []byte) (codec CodecData, err error) {
	if codec.FrameHeader, err = ParseFrameHeader(data); err != nil {
		return
	}
	codec.CodecType_ = av.AC3
	if codec.EAC3 {
		codec.CodecType_ = av.EAC3
		if codec.StreamType == 1 {
			err = fmt.Errorf("ac3parser: the stream starts with a dependent substream")
			return
		}
	}
	return
}

func (codec CodecData) Type() av.CodecType {
	return codec.CodecType_
}

func (codec CodecData) SampleRate() int {
	return codec.FrameHeader.SampleRate
}

func (codec CodecData) SampleFormat() av.SampleFormat {
	return av.FLTP
}

func (codec CodecData) ChannelLayout() av.ChannelLayout {
	return codec.FrameHeader.ChannelLayout()
}

// PacketDuration returns the duration of the first syncframe of the packet.
func (codec CodecData) PacketDuration(data []byte) (time.Duration, error) {
	h, err := ParseFrameHeader(data)
	if err != nil {
		return 0, err
	}
	return time.Duration(h.Samples()) * time.Second / time.Duration(h.SampleRate), nil
}

type bitReader struct {
	data []byte
	pos  int
}

// read returns the next n bits, 0 for the bits after the end of the data.
func (r *bitReader) read(n int) int {
	v := 0
	for ; n > 0; n-- {
		v <<= 1
		if r.pos/8 < len(r.data) && r.data[r.pos/8]&(0x80>>(r.pos%8)) != 0 {
			v |= 1
		}
		r.pos++
	}
	return v
}
