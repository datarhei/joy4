// Package flacparser parses the STREAMINFO metadata block and the frame header of FLAC (RFC 9639).
package flacparser

import (
	"encoding/binary"
	"fmt"
	"time"

	"github.com/datarhei/joy4/av"
)

// StreamInfoSize is the size of the STREAMINFO metadata block without its header.
const StreamInfoSize = 34

// CodecData is the codec data of a FLAC stream.
type CodecData struct {
	// the sequence header as it was received (see NewCodecDataFromSequenceHeader), nil if the codec data was
	// created from the STREAMINFO
	Header []byte
	// the STREAMINFO metadata block without its header
	StreamInfo []byte

	MinBlockSize   int
	MaxBlockSize   int
	MinFrameSize   int
	MaxFrameSize   int
	SampleRate_    int
	Channels       int
	BitsPerSample  int
	TotalSamples   uint64
	ChannelLayout_ av.ChannelLayout
}

func (codec CodecData) Type() av.CodecType {
	return av.FLAC
}

func (codec CodecData) SampleRate() int {
	return codec.SampleRate_
}

// SampleFormat returns the sample format of the decoded samples: S16 for up to 16 bits per sample, S32 otherwise.
func (codec CodecData) SampleFormat() av.SampleFormat {
	if codec.BitsPerSample <= 16 {
		return av.S16
	}
	return av.S32
}

func (codec CodecData) ChannelLayout() av.ChannelLayout {
	return codec.ChannelLayout_
}

// StreamInfoBytes returns the STREAMINFO metadata block without its header.
func (codec CodecData) StreamInfoBytes() []byte {
	return codec.StreamInfo
}

// SequenceHeaderBytes returns the sequence header as it was received, in either form. It is not normalized,
// because FFmpeg passes it on as extradata, and its MP4 muxer only accepts the form that FFmpeg writes (the
// STREAMINFO alone). Without one, it is the "fLaC" marker followed by the STREAMINFO metadata block as the
// last metadata block, the FLAC sequence start of E-RTMP v2.
func (codec CodecData) SequenceHeaderBytes() []byte {
	if codec.Header != nil {
		return codec.Header
	}
	b := make([]byte, 8+StreamInfoSize)
	copy(b, "fLaC")
	b[4] = 0x80 // last metadata block, type STREAMINFO (0)
	b[7] = StreamInfoSize
	copy(b[8:], codec.StreamInfo)
	return b
}

// PacketDuration returns the duration of the first frame of the packet, from the block size in its header.
func (codec CodecData) PacketDuration(data []byte) (time.Duration, error) {
	blockSize, err := FrameBlockSize(data)
	if err != nil {
		return 0, err
	}
	return time.Duration(blockSize) * time.Second / time.Duration(codec.SampleRate_), nil
}

// ChannelLayout returns the channel layout of the channel count, in the order that FLAC defines for the
// independent channel assignment.
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
		return av.CH_SURROUND | av.CH_LOW_FREQ | av.CH_BACK_LEFT | av.CH_BACK_RIGHT, nil
	case 7:
		return av.CH_SURROUND | av.CH_LOW_FREQ | av.CH_BACK_CENTER | av.CH_SIDE_LEFT | av.CH_SIDE_RIGHT, nil
	case 8:
		return av.CH_SURROUND | av.CH_LOW_FREQ | av.CH_BACK_LEFT | av.CH_BACK_RIGHT | av.CH_SIDE_LEFT | av.CH_SIDE_RIGHT, nil
	}
	return 0, fmt.Errorf("flacparser: unsupported channel count %d", channels)
}

// NewCodecDataFromSequenceHeader returns the codec data of a FLAC sequence header: the "fLaC" marker followed
// by the metadata blocks, of which STREAMINFO is the first (E-RTMP v2), or the STREAMINFO metadata block
// alone, without its header, as FFmpeg writes it.
func NewCodecDataFromSequenceHeader(b []byte) (codec CodecData, err error) {
	defer func() {
		if err == nil {
			codec.Header = append([]byte(nil), b...)
		}
	}()

	if len(b) == StreamInfoSize {
		return NewCodecDataFromStreamInfo(b)
	}
	if len(b) < 4 || string(b[:4]) != "fLaC" {
		err = fmt.Errorf("flacparser: invalid sequence header")
		return
	}
	if len(b) < 8 {
		err = fmt.Errorf("flacparser: invalid sequence header: no metadata block")
		return
	}
	if b[4]&0x7f != 0 {
		err = fmt.Errorf("flacparser: invalid sequence header: the first metadata block is not STREAMINFO")
		return
	}
	size := int(b[5])<<16 | int(b[6])<<8 | int(b[7])
	if size != StreamInfoSize || len(b) < 8+size {
		err = fmt.Errorf("flacparser: invalid STREAMINFO size")
		return
	}
	return NewCodecDataFromStreamInfo(b[8 : 8+size])
}

// NewCodecDataFromStreamInfo returns the codec data of the STREAMINFO metadata block without its header.
func NewCodecDataFromStreamInfo(b []byte) (codec CodecData, err error) {
	if len(b) != StreamInfoSize {
		err = fmt.Errorf("flacparser: invalid STREAMINFO size %d", len(b))
		return
	}

	codec.StreamInfo = append([]byte(nil), b...)
	codec.MinBlockSize = int(binary.BigEndian.Uint16(b[0:]))
	codec.MaxBlockSize = int(binary.BigEndian.Uint16(b[2:]))
	codec.MinFrameSize = int(b[4])<<16 | int(b[5])<<8 | int(b[6])
	codec.MaxFrameSize = int(b[7])<<16 | int(b[8])<<8 | int(b[9])
	// sample rate (20 bits), channels - 1 (3 bits), bits per sample - 1 (5 bits), total samples (36 bits)
	v := binary.BigEndian.Uint64(b[10:])
	codec.SampleRate_ = int(v >> 44)
	codec.Channels = int(v>>41&0x07) + 1
	codec.BitsPerSample = int(v>>36&0x1f) + 1
	codec.TotalSamples = v & 0xfffffffff

	if codec.SampleRate_ == 0 {
		err = fmt.Errorf("flacparser: invalid STREAMINFO: no sample rate")
		return
	}
	if codec.MinBlockSize < 16 || codec.MaxBlockSize < codec.MinBlockSize {
		err = fmt.Errorf("flacparser: invalid STREAMINFO: invalid block sizes %d, %d", codec.MinBlockSize, codec.MaxBlockSize)
		return
	}
	if codec.ChannelLayout_, err = ChannelLayout(codec.Channels); err != nil {
		return
	}

	return
}

// FrameBlockSize returns the block size (samples per channel) of a frame from its header.
func FrameBlockSize(data []byte) (int, error) {
	if len(data) < 5 || data[0] != 0xff || data[1]&0xfe != 0xf8 {
		return 0, fmt.Errorf("flacparser: no frame header")
	}

	code := data[2] >> 4
	switch {
	case code == 0:
		return 0, fmt.Errorf("flacparser: reserved block size")
	case code == 1:
		return 192, nil
	case code <= 5:
		return 576 << (code - 2), nil
	case code >= 8:
		return 256 << (code - 8), nil
	}

	// the block size - 1 follows the coded frame or sample number, which is coded like UTF-8 with up to 7 bytes
	ones := 0
	for b := data[4]; b&0x80 != 0; b <<= 1 {
		ones++
	}
	n := 1
	switch {
	case ones == 1 || ones > 7:
		return 0, fmt.Errorf("flacparser: invalid coded number")
	case ones > 1:
		n = ones
	}
	pos := 4 + n
	if code == 6 {
		if len(data) < pos+1 {
			return 0, fmt.Errorf("flacparser: frame header too short")
		}
		return int(data[pos]) + 1, nil
	}
	if len(data) < pos+2 {
		return 0, fmt.Errorf("flacparser: frame header too short")
	}
	return int(binary.BigEndian.Uint16(data[pos:])) + 1, nil
}
