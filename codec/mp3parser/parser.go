// Package mp3parser parses the frame header of MP3 (MPEG-1, MPEG-2 and MPEG-2.5 audio layer III).
package mp3parser

import (
	"fmt"
	"time"

	"github.com/datarhei/joy4/av"
)

// MPEG audio versions
const (
	MPEG1  = 1
	MPEG2  = 2
	MPEG25 = 25
)

// FrameHeader is the header of an MP3 frame.
type FrameHeader struct {
	// MPEG1, MPEG2 or MPEG25
	Version    int
	SampleRate int
	// the bit rate in kbit/s, 0 for the free format
	BitRate int
	// the channel mode: 0 stereo, 1 joint stereo, 2 dual channel, 3 mono
	ChannelMode int
}

// Samples returns the number of samples per channel of the frame: 1152 for MPEG-1, 576 for MPEG-2 and 2.5.
func (h FrameHeader) Samples() int {
	if h.Version == MPEG1 {
		return 1152
	}
	return 576
}

// ChannelLayout returns mono for the channel mode mono, stereo otherwise.
func (h FrameHeader) ChannelLayout() av.ChannelLayout {
	if h.ChannelMode == 3 {
		return av.CH_MONO
	}
	return av.CH_STEREO
}

var sampleRates = [3]int{44100, 48000, 32000}

// the bit rates of layer III in kbit/s
var bitRates = map[int][15]int{
	MPEG1: {0, 32, 40, 48, 56, 64, 80, 96, 112, 128, 160, 192, 224, 256, 320},
	MPEG2: {0, 8, 16, 24, 32, 40, 48, 56, 64, 80, 96, 112, 128, 144, 160},
}

// ParseFrameHeader parses the header of the frame at the start of data.
func ParseFrameHeader(data []byte) (h FrameHeader, err error) {
	if len(data) < 4 || data[0] != 0xff || data[1]&0xe0 != 0xe0 {
		err = fmt.Errorf("mp3parser: no frame header")
		return
	}

	switch data[1] >> 3 & 0x03 {
	case 0:
		h.Version = MPEG25
	case 2:
		h.Version = MPEG2
	case 3:
		h.Version = MPEG1
	default:
		err = fmt.Errorf("mp3parser: reserved version")
		return
	}

	if layer := data[1] >> 1 & 0x03; layer != 1 {
		err = fmt.Errorf("mp3parser: not layer III")
		return
	}

	index := int(data[2] >> 4)
	if index == 15 {
		err = fmt.Errorf("mp3parser: invalid bit rate")
		return
	}
	if h.Version == MPEG1 {
		h.BitRate = bitRates[MPEG1][index]
	} else {
		h.BitRate = bitRates[MPEG2][index]
	}

	rate := int(data[2] >> 2 & 0x03)
	if rate == 3 {
		err = fmt.Errorf("mp3parser: reserved sample rate")
		return
	}
	switch h.Version {
	case MPEG1:
		h.SampleRate = sampleRates[rate]
	case MPEG2:
		h.SampleRate = sampleRates[rate] / 2
	case MPEG25:
		h.SampleRate = sampleRates[rate] / 4
	}

	h.ChannelMode = int(data[3] >> 6)

	return
}

// CodecData is the codec data of an MP3 stream, from its first frame.
type CodecData struct {
	FrameHeader
}

// NewCodecDataFromFrame returns the codec data of the frame at the start of data.
func NewCodecDataFromFrame(data []byte) (codec CodecData, err error) {
	codec.FrameHeader, err = ParseFrameHeader(data)
	return
}

func (codec CodecData) Type() av.CodecType {
	return av.MP3
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

// PacketDuration returns the duration of the first frame of the packet.
func (codec CodecData) PacketDuration(data []byte) (time.Duration, error) {
	h, err := ParseFrameHeader(data)
	if err != nil {
		return 0, err
	}
	return time.Duration(h.Samples()) * time.Second / time.Duration(h.SampleRate), nil
}
