package hevcparser

import (
	"encoding/hex"
	"testing"

	"github.com/stretchr/testify/require"
)

// records of FFmpeg (libx265): the header (23 bytes) and the VPS, SPS and PPS. FFmpeg's records have an
// SEI array after them, which is left out here.
var ffmpegRecords = []struct {
	name          string
	header        string
	vps, sps, pps string
	width, height int
}{
	{
		name:   "main 4:2:0 8 bit",
		header: "0101600000009000000000003ff000fcfdf8f800000f04",
		vps:    "40010c01ffff01600000030090000003000003003fba0240",
		sps:    "42010101600000030090000003000003003fa005020171f2e5ba4a4c2f016808000003000800000300c840",
		pps:    "4401c073c189",
		width:  640, height: 360,
	},
	{
		name:   "main10 4:2:0 10 bit",
		header: "0102200000009000000000003ff000fcfdfafa00000f04",
		vps:    "40010c01ffff02200000030090000003000003003f959409",
		sps:    "42010102200000030090000003000003003fa005020171f2b6595952930bc05a020000030002000003003210",
		pps:    "4401c073c189",
		width:  640, height: 360,
	},
	{
		name:   "rext 4:4:4 8 bit",
		header: "0104080000009e08000000003cf000fcfff8f800000f04",
		vps:    "40010c01ffff0408000003009e0800000300003c959409",
		sps:    "4201010408000003009e0800000300003c90014101e2cb2b2a5261780b7020200040000003004000000642",
		pps:    "4401c07318301890",
		width:  320, height: 240,
	},
}

func decodeHex(t *testing.T, s string) []byte {
	b, err := hex.DecodeString(s)
	require.NoError(t, err)
	return b
}

// array returns an hvcC NAL unit array with the NAL units.
func array(naluType byte, nalus ...[]byte) []byte {
	b := []byte{naluType, byte(len(nalus) >> 8), byte(len(nalus))}
	for _, nalu := range nalus {
		b = append(b, byte(len(nalu)>>8), byte(len(nalu)))
		b = append(b, nalu...)
	}
	return b
}

func TestHEVCDecoderConfRecordFFmpeg(t *testing.T) {
	for _, r := range ffmpegRecords {
		header := decodeHex(t, r.header)
		vps, sps, pps := decodeHex(t, r.vps), decodeHex(t, r.sps), decodeHex(t, r.pps)
		sei := []byte{0x4e, 0x01, 0x05, 0x01, 0xff, 0x80}

		// as written by FFmpeg, with an SEI array at the end
		record := append([]byte(nil), header...)
		record = append(record, array(NAL_UNIT_VPS, vps)...)
		record = append(record, array(NAL_UNIT_SPS, sps)...)
		record = append(record, array(NAL_UNIT_PPS, pps)...)
		record = append(record, array(39, sei)...)

		codecData, err := NewCodecDataFromHEVCDecoderConfRecord(record)
		require.NoError(t, err, r.name)
		info := codecData.RecordInfo
		require.Equal(t, uint8(3), info.LengthSizeMinusOne, r.name)
		require.Equal(t, [][]byte{vps}, info.VPS, r.name)
		require.Equal(t, [][]byte{sps}, info.SPS, r.name)
		require.Equal(t, [][]byte{pps}, info.PPS, r.name)
		require.Equal(t, header[1], info.HEVCProfileIndication, r.name)
		require.Equal(t, header[2], info.ProfileCompatibility, r.name)
		require.Equal(t, header[12], info.HEVCLevelIndication, r.name)
		require.Equal(t, uint8(1), info.NumTemporalLayers, r.name)
		require.Equal(t, uint8(1), info.TemporalIDNested, r.name)
		require.Equal(t, r.width, codecData.Width(), r.name)
		require.Equal(t, r.height, codecData.Height(), r.name)
		require.Equal(t, uint(header[12]), codecData.SPSInfo.LevelIdc, r.name)
		require.Equal(t, uint(header[1]&0x1f), codecData.SPSInfo.ProfileIdc, r.name)

		// a record built from the parameter sets has the same header as the one of FFmpeg, except for
		// the number of arrays (FFmpeg has the SEI array)
		codecData, err = NewCodecDataFromVPSAndSPSAndPPS(vps, sps, pps)
		require.NoError(t, err, r.name)
		expected := append([]byte(nil), header[:22]...)
		expected = append(expected, 3)
		expected = append(expected, array(NAL_UNIT_VPS, vps)...)
		expected = append(expected, array(NAL_UNIT_SPS, sps)...)
		expected = append(expected, array(NAL_UNIT_PPS, pps)...)
		require.Equal(t, expected, codecData.HEVCDecoderConfRecordBytes(), r.name)

		// and it is read back the same
		readBack, err := NewCodecDataFromHEVCDecoderConfRecord(codecData.HEVCDecoderConfRecordBytes())
		require.NoError(t, err, r.name)
		require.Equal(t, codecData.RecordInfo, readBack.RecordInfo, r.name)
	}
}

func TestHEVCDecoderConfRecordArrays(t *testing.T) {
	r := ffmpegRecords[0]
	header := decodeHex(t, r.header)
	vps, sps, pps := decodeHex(t, r.vps), decodeHex(t, r.sps), decodeHex(t, r.pps)
	sei := []byte{0x4e, 0x01, 0x05, 0x01, 0xff, 0x80}

	// an SEI array first, the arrays in another order, two PPS
	record := append([]byte(nil), header[:22]...)
	record = append(record, 4)
	record = append(record, array(39, sei, sei)...)
	record = append(record, array(0x80|NAL_UNIT_SPS, sps)...)
	record = append(record, array(NAL_UNIT_PPS, pps, pps)...)
	record = append(record, array(NAL_UNIT_VPS, vps)...)

	var info HEVCDecoderConfRecord
	n, err := info.Unmarshal(record)
	require.NoError(t, err)
	require.Equal(t, len(record), n)
	require.Equal(t, [][]byte{vps}, info.VPS)
	require.Equal(t, [][]byte{sps}, info.SPS)
	require.Equal(t, [][]byte{pps, pps}, info.PPS)

	// Marshal writes the VPS, SPS and PPS arrays
	b := make([]byte, info.Len())
	require.Equal(t, len(b), info.Marshal(b, SPSInfo{}))
	expected := append([]byte(nil), header[:22]...)
	expected = append(expected, 3)
	expected = append(expected, array(NAL_UNIT_VPS, vps)...)
	expected = append(expected, array(NAL_UNIT_SPS, sps)...)
	expected = append(expected, array(NAL_UNIT_PPS, pps, pps)...)
	require.Equal(t, expected, b)

	// truncated records
	for i := 0; i < len(record); i++ {
		_, err := info.Unmarshal(record[:i])
		require.Error(t, err, "length %d", i)
	}
}
