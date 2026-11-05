package hevcparser

import (
	"encoding/hex"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestHEVCDecoderConfRecordLengthSize(t *testing.T) {
	// the header of a record of FFmpeg (libx265), with the VPS, SPS and PPS of 320x240
	record, err := hex.DecodeString("0104080000009e08000000003cf000fcfff8f800000f03" +
		"200001001740010c01ffff0408000003009e0800000300003c959409" +
		"210001002b4201010408000003009e0800000300003c90014101e2cb2b2a5261780b7020200040000003004000000642" +
		"22000100084401c07318301890")
	require.NoError(t, err)

	codecData, err := NewCodecDataFromHEVCDecoderConfRecord(record)
	require.NoError(t, err)
	require.Equal(t, uint8(3), codecData.RecordInfo.LengthSizeMinusOne)
	require.Len(t, codecData.RecordInfo.VPS, 1)
	require.Len(t, codecData.RecordInfo.SPS, 1)
	require.Len(t, codecData.RecordInfo.PPS, 1)
	require.Equal(t, 320, codecData.Width())
	require.Equal(t, 240, codecData.Height())

	// a record written by Marshal
	codecData, err = NewCodecDataFromVPSAndSPSAndPPS(codecData.VPS(), codecData.SPS(), codecData.PPS())
	require.NoError(t, err)
	codecData, err = NewCodecDataFromHEVCDecoderConfRecord(codecData.HEVCDecoderConfRecordBytes())
	require.NoError(t, err)
	require.Equal(t, uint8(3), codecData.RecordInfo.LengthSizeMinusOne)
}
