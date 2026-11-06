// Package screenvideoparser parses the header of Screen video frames (FLV video codec ids 3 and 6).
package screenvideoparser

import (
	"fmt"

	"github.com/datarhei/joy4/av"
)

// FrameHeader is the start of a Screen video (v1 and v2) frame.
type FrameHeader struct {
	BlockWidth  int
	BlockHeight int
	Width       int
	Height      int
}

// ParseFrameHeader parses the header at the start of a frame: the block width / 16 - 1 (UB4), the image width
// (UB12), the block height / 16 - 1 (UB4) and the image height (UB12).
func ParseFrameHeader(data []byte) (h FrameHeader, err error) {
	if len(data) < 4 {
		err = fmt.Errorf("screenvideoparser: frame header too short")
		return
	}
	h.BlockWidth = (int(data[0]>>4) + 1) * 16
	h.Width = int(data[0]&0x0f)<<8 | int(data[1])
	h.BlockHeight = (int(data[2]>>4) + 1) * 16
	h.Height = int(data[2]&0x0f)<<8 | int(data[3])
	if h.Width == 0 || h.Height == 0 {
		err = fmt.Errorf("screenvideoparser: invalid image size %dx%d", h.Width, h.Height)
		return
	}
	return
}

// CodecData is the codec data of a Screen video stream, from the header of its first frame.
type CodecData struct {
	// av.FLASHSV or av.FLASHSV2
	CodecType_ av.CodecType
	Width_     int
	Height_    int
}

// NewCodecDataFromFrame returns the codec data of the frame header at the start of a frame of Screen video
// (av.FLASHSV) or Screen video v2 (av.FLASHSV2).
func NewCodecDataFromFrame(typ av.CodecType, data []byte) (codec CodecData, err error) {
	if typ != av.FLASHSV && typ != av.FLASHSV2 {
		err = fmt.Errorf("screenvideoparser: not a Screen video codec: %v", typ)
		return
	}
	h, err := ParseFrameHeader(data)
	if err != nil {
		return
	}
	codec.CodecType_ = typ
	codec.Width_ = h.Width
	codec.Height_ = h.Height
	return
}

func (codec CodecData) Type() av.CodecType {
	return codec.CodecType_
}

func (codec CodecData) Width() int {
	return codec.Width_
}

func (codec CodecData) Height() int {
	return codec.Height_
}
