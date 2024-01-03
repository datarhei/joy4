package av1parser

import (
	"github.com/datarhei/joy4/av"
)

type CodecData struct {
	Record []byte
}

func (codec CodecData) Type() av.CodecType {
	return av.AV1
}

func (codec CodecData) AV1DecoderConfRecordBytes() []byte {
	return codec.Record
}

func (codec CodecData) Width() int {
	return 0
}

func (codec CodecData) Height() int {
	return 0
}

func NewCodecDataFromAV1DecoderConfRecord(record []byte) (self CodecData, err error) {
	self.Record = record

	return
}
