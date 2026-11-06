// Package h263parser parses the picture header of Sorenson H.263 (Sorenson Spark, FLV video codec id 2).
package h263parser

import (
	"fmt"

	"github.com/datarhei/joy4/av"
)

// Picture types
const (
	PictureIntra = 0
	PictureInter = 1
	// an inter picture that no other picture refers to (disposable inter frame of FLV)
	PictureDisposable = 2
)

// PictureHeader is the picture header of a Sorenson H.263 frame.
type PictureHeader struct {
	// 0 or 1
	Version     int
	Width       int
	Height      int
	PictureType int
}

// the picture sizes of the size codes 2 to 6
var pictureSizes = [7][2]int{2: {352, 288}, 3: {176, 144}, 4: {128, 96}, 5: {320, 240}, 6: {160, 120}}

// ParsePictureHeader parses the picture header at the start of a frame.
func ParsePictureHeader(data []byte) (h PictureHeader, err error) {
	r := bitReader{data: data}

	// picture start code (17 bits)
	if len(data) < 4 || r.read(17) != 1 {
		err = fmt.Errorf("h263parser: no picture start code")
		return
	}
	if h.Version = r.read(5); h.Version > 1 {
		err = fmt.Errorf("h263parser: unsupported version %d", h.Version)
		return
	}
	r.read(8) // temporal reference

	bits := 0
	switch code := r.read(3); code {
	case 0:
		bits = 8
	case 1:
		bits = 16
	case 7:
		err = fmt.Errorf("h263parser: reserved picture size")
		return
	default:
		h.Width, h.Height = pictureSizes[code][0], pictureSizes[code][1]
	}
	if bits != 0 {
		if r.bits()+2*bits+2 > 8*len(data) {
			err = fmt.Errorf("h263parser: picture header too short")
			return
		}
		h.Width = r.read(bits)
		h.Height = r.read(bits)
	}
	if h.Width == 0 || h.Height == 0 {
		err = fmt.Errorf("h263parser: invalid picture size %dx%d", h.Width, h.Height)
		return
	}

	if r.bits()+2 > 8*len(data) {
		err = fmt.Errorf("h263parser: picture header too short")
		return
	}
	if h.PictureType = r.read(2); h.PictureType > PictureDisposable {
		err = fmt.Errorf("h263parser: reserved picture type")
		return
	}

	return
}

// CodecData is the codec data of a Sorenson H.263 stream, from the picture header of its first frame.
type CodecData struct {
	Version int
	Width_  int
	Height_ int
}

// NewCodecDataFromFrame returns the codec data of the picture header at the start of a frame.
func NewCodecDataFromFrame(data []byte) (codec CodecData, err error) {
	h, err := ParsePictureHeader(data)
	if err != nil {
		return
	}
	codec.Version = h.Version
	codec.Width_ = h.Width
	codec.Height_ = h.Height
	return
}

func (codec CodecData) Type() av.CodecType {
	return av.H263
}

func (codec CodecData) Width() int {
	return codec.Width_
}

func (codec CodecData) Height() int {
	return codec.Height_
}

// IsDisposable returns whether the frame is a disposable inter frame.
func IsDisposable(data []byte) bool {
	h, err := ParsePictureHeader(data)
	return err == nil && h.PictureType == PictureDisposable
}

type bitReader struct {
	data []byte
	pos  int
}

func (r *bitReader) bits() int {
	return r.pos
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
