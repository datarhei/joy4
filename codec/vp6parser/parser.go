// Package vp6parser parses the frame header of On2 VP6 (FLV video codec ids 4 and 5, VP6 with alpha).
package vp6parser

import (
	"fmt"

	"github.com/datarhei/joy4/av"
)

// FrameHeader is the header of a VP6 frame.
type FrameHeader struct {
	KeyFrame bool
	// key frames only: the version, and the number of macroblock rows and columns
	Version int
	Rows    int
	Cols    int
}

// ParseFrameHeader parses the header of the VP6 frame at the start of data. The rows and columns are only
// in a key frame.
func ParseFrameHeader(data []byte) (h FrameHeader, err error) {
	if len(data) < 1 {
		err = fmt.Errorf("vp6parser: empty frame")
		return
	}
	h.KeyFrame = data[0]&0x80 == 0
	if !h.KeyFrame {
		return
	}

	if len(data) < 2 {
		err = fmt.Errorf("vp6parser: key frame header too short")
		return
	}
	separatedCoeff := data[0]&0x01 != 0
	h.Version = int(data[1] >> 3)
	if h.Version > 8 {
		err = fmt.Errorf("vp6parser: unsupported version %d", h.Version)
		return
	}
	filterHeader := data[1] & 0x06

	// the offset of the coefficients (UI16) precedes the dimensions
	pos := 2
	if separatedCoeff || filterHeader == 0 {
		pos += 2
	}
	if len(data) < pos+2 {
		err = fmt.Errorf("vp6parser: key frame header too short")
		return
	}
	h.Rows = int(data[pos])
	h.Cols = int(data[pos+1])
	if h.Rows == 0 || h.Cols == 0 {
		err = fmt.Errorf("vp6parser: invalid size of %dx%d macroblocks", h.Cols, h.Rows)
		return
	}
	return
}

// AlphaOffsetSize is the size of the offset of the alpha data (UI24) at the start of a frame of VP6 with alpha.
const AlphaOffsetSize = 3

// CodecData is the codec data of a VP6 stream, from its size adjustment and its first key frame.
type CodecData struct {
	// av.VP6 or av.VP6A
	CodecType_ av.CodecType
	// the size adjustment of FLV: the horizontal (high 4 bits) and the vertical (low 4 bits) number of pixels to
	// crop from the macroblocks
	Adjustment uint8
	Rows       int
	Cols       int
}

// NewCodecDataFromFrame returns the codec data of a VP6 (av.VP6) or VP6 with alpha (av.VP6A) stream from the
// size adjustment and a key frame. The frame of VP6 with alpha starts with the offset of the alpha data.
func NewCodecDataFromFrame(typ av.CodecType, adjustment uint8, data []byte) (codec CodecData, err error) {
	switch typ {
	case av.VP6:
	case av.VP6A:
		if len(data) < AlphaOffsetSize {
			err = fmt.Errorf("vp6parser: no alpha offset")
			return
		}
		data = data[AlphaOffsetSize:]
	default:
		err = fmt.Errorf("vp6parser: not a VP6 codec: %v", typ)
		return
	}

	h, err := ParseFrameHeader(data)
	if err != nil {
		return
	}
	if !h.KeyFrame {
		err = fmt.Errorf("vp6parser: not a key frame")
		return
	}
	codec.CodecType_ = typ
	codec.Adjustment = adjustment
	codec.Rows = h.Rows
	codec.Cols = h.Cols
	return
}

func (codec CodecData) Type() av.CodecType {
	return codec.CodecType_
}

// Width returns the width of the macroblocks minus the horizontal adjustment (at most 15 pixels, so it is
// positive).
func (codec CodecData) Width() int {
	return 16*codec.Cols - int(codec.Adjustment>>4)
}

// Height returns the height of the macroblocks minus the vertical adjustment.
func (codec CodecData) Height() int {
	return 16*codec.Rows - int(codec.Adjustment&0x0f)
}
