// Package opusparser parses the Opus ID header (RFC 7845) and the TOC byte of Opus packets (RFC 6716).
package opusparser

import (
	"encoding/binary"
	"fmt"
	"time"

	"github.com/datarhei/joy4/av"
)

// SampleRate is the sample rate of decoded Opus, always 48 kHz.
const SampleRate = 48000

// CodecData is the codec data of an Opus stream.
type CodecData struct {
	// the ID header ("OpusHead", RFC 7845 5.1), empty if the stream has none
	Header []byte

	Channels             int
	PreSkip              int
	InputSampleRate      int
	OutputGain           int16
	ChannelMappingFamily int
	StreamCount          int
	CoupledCount         int
	ChannelMapping       []uint8
	ChannelLayout_       av.ChannelLayout
}

func (codec CodecData) Type() av.CodecType {
	return av.OPUS
}

func (codec CodecData) SampleRate() int {
	return SampleRate
}

func (codec CodecData) SampleFormat() av.SampleFormat {
	return av.FLT
}

func (codec CodecData) ChannelLayout() av.ChannelLayout {
	return codec.ChannelLayout_
}

// OpusHeadBytes returns the ID header. A stream without one (see NewCodecData) gets a minimal ID header with
// its channels (mono or stereo, channel mapping family 0), without pre-skip.
func (codec CodecData) OpusHeadBytes() []byte {
	if codec.Header != nil {
		return codec.Header
	}
	header := make([]byte, 19)
	copy(header, "OpusHead")
	header[8] = 1
	header[9] = uint8(codec.Channels)
	binary.LittleEndian.PutUint16(header[10:], uint16(codec.PreSkip))
	binary.LittleEndian.PutUint32(header[12:], SampleRate)
	return header
}

// PacketDuration returns the duration of an Opus packet. With several streams (multichannel), the packets of
// all streams have the same duration, the TOC byte of the first one is used.
func (codec CodecData) PacketDuration(data []byte) (time.Duration, error) {
	return PacketDuration(data)
}

// frameDurations are the frame durations of the configurations (TOC byte, RFC 6716 3.1) in units of 100 µs.
var frameDurations = [32]int{
	// SILK
	100, 200, 400, 600, 100, 200, 400, 600, 100, 200, 400, 600,
	// Hybrid
	100, 200, 100, 200,
	// CELT
	25, 50, 100, 200, 25, 50, 100, 200, 25, 50, 100, 200, 25, 50, 100, 200,
}

// PacketDuration returns the duration of an Opus packet from its TOC byte and frame count (RFC 6716 3.1, 3.2).
func PacketDuration(data []byte) (time.Duration, error) {
	if len(data) < 1 {
		return 0, fmt.Errorf("opusparser: empty packet")
	}
	toc := data[0]
	frames := 1
	switch toc & 0x03 {
	case 1, 2:
		frames = 2
	case 3:
		if len(data) < 2 {
			return 0, fmt.Errorf("opusparser: packet without frame count")
		}
		frames = int(data[1] & 0x3f)
	}
	return time.Duration(frames*frameDurations[toc>>3]) * 100 * time.Microsecond, nil
}

// IsStereo returns whether an Opus packet is stereo (the s bit of the TOC byte).
func IsStereo(data []byte) bool {
	return len(data) != 0 && data[0]&0x04 != 0
}

// ChannelLayout returns the channel layout of the channel count, in the order of the Vorbis channel mapping
// (RFC 7845 5.1.1.2), which is the one of Opus with the channel mapping families 0 and 1.
func ChannelLayout(channels int) (av.ChannelLayout, error) {
	switch channels {
	case 1:
		return av.CH_MONO, nil
	case 2:
		return av.CH_STEREO, nil
	case 3:
		return av.CH_SURROUND, nil
	case 4:
		return av.CH_STEREO | av.CH_BACK_LEFT | av.CH_BACK_RIGHT, nil
	case 5:
		return av.CH_SURROUND | av.CH_BACK_LEFT | av.CH_BACK_RIGHT, nil
	case 6:
		return av.CH_SURROUND | av.CH_BACK_LEFT | av.CH_BACK_RIGHT | av.CH_LOW_FREQ, nil
	case 7:
		return av.CH_SURROUND | av.CH_SIDE_LEFT | av.CH_SIDE_RIGHT | av.CH_BACK_CENTER | av.CH_LOW_FREQ, nil
	case 8:
		return av.CH_SURROUND | av.CH_SIDE_LEFT | av.CH_SIDE_RIGHT | av.CH_BACK_LEFT | av.CH_BACK_RIGHT | av.CH_LOW_FREQ, nil
	}
	return 0, fmt.Errorf("opusparser: unsupported channel count %d", channels)
}

// NewCodecDataFromOpusHead returns the codec data of an ID header ("OpusHead", RFC 7845 5.1).
func NewCodecDataFromOpusHead(header []byte) (codec CodecData, err error) {
	if len(header) < 19 || string(header[:8]) != "OpusHead" {
		err = fmt.Errorf("opusparser: invalid ID header")
		return
	}
	if header[8]>>4 != 0 {
		// the major version must be 0
		err = fmt.Errorf("opusparser: unsupported ID header version %d", header[8])
		return
	}

	codec.Header = header
	codec.Channels = int(header[9])
	codec.PreSkip = int(binary.LittleEndian.Uint16(header[10:]))
	codec.InputSampleRate = int(binary.LittleEndian.Uint32(header[12:]))
	codec.OutputGain = int16(binary.LittleEndian.Uint16(header[16:]))
	codec.ChannelMappingFamily = int(header[18])

	if codec.Channels == 0 {
		err = fmt.Errorf("opusparser: invalid ID header: no channels")
		return
	}

	if codec.ChannelMappingFamily == 0 {
		if codec.Channels > 2 {
			err = fmt.Errorf("opusparser: invalid ID header: %d channels with the channel mapping family 0", codec.Channels)
			return
		}
		codec.StreamCount = 1
		codec.CoupledCount = codec.Channels - 1
	} else {
		if len(header) < 21+codec.Channels {
			err = fmt.Errorf("opusparser: invalid ID header: not enough bytes for the channel mapping table")
			return
		}
		codec.StreamCount = int(header[19])
		codec.CoupledCount = int(header[20])
		codec.ChannelMapping = append([]uint8(nil), header[21:21+codec.Channels]...)
		if codec.StreamCount == 0 || codec.CoupledCount > codec.StreamCount {
			err = fmt.Errorf("opusparser: invalid ID header: invalid stream counts")
			return
		}
	}

	if codec.ChannelLayout_, err = ChannelLayout(codec.Channels); err != nil {
		return
	}

	return
}

// NewCodecData returns the codec data of a stream without an ID header, e.g. from the channel count of an
// E-RTMP v2 MultichannelConfig or the stereo flag of the first packet. It has no ID header.
func NewCodecData(channels int) (codec CodecData, err error) {
	if channels < 1 || channels > 2 {
		// without an ID header, only mono and stereo are defined (one stream)
		err = fmt.Errorf("opusparser: unsupported channel count %d without an ID header", channels)
		return
	}
	codec.Channels = channels
	codec.StreamCount = 1
	codec.CoupledCount = channels - 1
	codec.ChannelLayout_, err = ChannelLayout(channels)
	return
}
