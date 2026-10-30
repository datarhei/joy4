package h264parser

import (
	"encoding/hex"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestParser(t *testing.T) {
	var typ int
	var nalus [][]byte

	annexbFrame, _ := hex.DecodeString("00000001223322330000000122332233223300000133000001000001")
	nalus, typ = SplitNALUs(annexbFrame)
	t.Log(typ, len(nalus))

	avccFrame, _ := hex.DecodeString(
		"00000008aabbccaabbccaabb00000001aa",
	)
	nalus, typ = SplitNALUs(avccFrame)
	t.Log(typ, len(nalus))
}

func TestParseSPSSize(t *testing.T) {
	// SPS written by FFmpeg/libx264, the expected size is the one reported by ffprobe
	for _, tc := range []struct {
		name          string
		sps           string
		width, height uint
	}{
		{"4:2:0", "6764001eacd940a02ff97011000003000100000300320f162d96", 640, 360},
		{"4:2:0 1080p", "67640028acd940780227e5c044000003000400000300c83c60c658", 1920, 1080},
		{"4:2:2", "677a001ebcd940a02ff89c0440000003004000000c83c58b6580", 640, 360},
		{"4:2:2 odd height", "677a001ebcd940a02ff88c0440000003004000000c83c58b6580", 640, 361},
		{"4:4:4", "67f4001e919b281405ff1380880000030008000003019078b16cb0", 640, 360},
		{"4:4:4 odd size", "67f4001e919b281485fc211180880000030008000003019078b16cb0", 641, 361},
		{"monochrome", "6764001ef3650280bfe27016c80000030008000003019078b16cb0", 640, 360},
		{"monochrome odd size", "6764001ef3650290bf84223016c80000030008000003019078b16cb0", 641, 361},
		{"4:2:0 interlaced", "6764001eacd940a063f3e022000003000200000300643e28532c", 640, 360},
		{"4:2:2 interlaced", "677a001ebcd940a063f1b8088000000300800000190f8a14cb", 640, 360},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b, err := hex.DecodeString(tc.sps)
			require.NoError(t, err)

			info, err := ParseSPS(b)
			require.NoError(t, err)
			require.Equal(t, tc.width, info.Width)
			require.Equal(t, tc.height, info.Height)
		})
	}
}

// spsWriter writes the bits of an SPS.
type spsWriter struct {
	bits []byte
}

func (w *spsWriter) u(n int, v uint) {
	for i := n - 1; i >= 0; i-- {
		w.bits = append(w.bits, byte(v>>uint(i))&1)
	}
}

func (w *spsWriter) ue(v uint) {
	v++
	n := 0
	for x := v; x > 1; x >>= 1 {
		n++
	}
	w.u(n, 0)
	w.u(n+1, v)
}

func (w *spsWriter) se(v int) {
	if v > 0 {
		w.ue(uint(2*v - 1))
	} else {
		w.ue(uint(-2 * v))
	}
}

func (w *spsWriter) bytes() []byte {
	// rbsp_stop_one_bit and alignment
	w.u(1, 1)
	for len(w.bits)%8 != 0 {
		w.u(1, 0)
	}
	b := make([]byte, len(w.bits)/8)
	for i, bit := range w.bits {
		b[i/8] |= bit << uint(7-i%8)
	}
	return b
}

type spsParams struct {
	profile                    uint
	chromaFormat               uint
	separateColourPlane        uint
	scalingLists               int // number of scaling lists in the SPS, 0 for none
	mbWidth, mbHeight          uint
	frameMbsOnly               uint
	cropL, cropR, cropT, cropB uint
}

// buildSPS returns an SPS (with NAL header) with the given parameters.
func buildSPS(p spsParams) []byte {
	w := &spsWriter{}
	w.u(8, 0x67)
	w.u(8, p.profile)
	w.u(8, 0) // constraint flags
	w.u(8, 30)
	w.ue(0) // seq_parameter_set_id
	if p.chromaFormat != 1 || p.profile != 66 {
		w.ue(p.chromaFormat)
		if p.chromaFormat == 3 {
			w.u(1, p.separateColourPlane)
		}
		w.ue(0)   // bit_depth_luma_minus8
		w.ue(0)   // bit_depth_chroma_minus8
		w.u(1, 0) // qpprime_y_zero_transform_bypass_flag
		if p.scalingLists > 0 {
			w.u(1, 1)
			for i := 0; i < p.scalingLists; i++ {
				w.u(1, 1) // seq_scaling_list_present_flag
				w.se(-8)  // delta_scale to a next scale of 0, the list ends
			}
		} else {
			w.u(1, 0)
		}
	}
	w.ue(0) // log2_max_frame_num_minus4
	w.ue(2) // pic_order_cnt_type
	w.ue(1) // max_num_ref_frames
	w.u(1, 0)
	w.ue(p.mbWidth - 1)
	w.ue(p.mbHeight - 1)
	w.u(1, p.frameMbsOnly)
	if p.frameMbsOnly == 0 {
		w.u(1, 0) // mb_adaptive_frame_field_flag
	}
	w.u(1, 1) // direct_8x8_inference_flag
	if p.cropL+p.cropR+p.cropT+p.cropB > 0 {
		w.u(1, 1)
		w.ue(p.cropL)
		w.ue(p.cropR)
		w.ue(p.cropT)
		w.ue(p.cropB)
	} else {
		w.u(1, 0)
	}
	w.u(1, 0) // vui_parameters_present_flag
	return w.bytes()
}

func TestParseSPSSizeSynthetic(t *testing.T) {
	for _, tc := range []struct {
		name          string
		params        spsParams
		width, height uint
	}{
		// 41x23 macroblocks, 656x368 pixels
		{"4:2:0", spsParams{profile: 100, chromaFormat: 1, mbWidth: 41, mbHeight: 23, frameMbsOnly: 1, cropL: 1, cropR: 2, cropT: 1, cropB: 3}, 650, 360},
		{"4:2:0 baseline", spsParams{profile: 66, chromaFormat: 1, mbWidth: 41, mbHeight: 23, frameMbsOnly: 1, cropR: 8, cropB: 4}, 640, 360},
		{"4:2:2", spsParams{profile: 122, chromaFormat: 2, mbWidth: 41, mbHeight: 23, frameMbsOnly: 1, cropL: 1, cropR: 2, cropT: 1, cropB: 3}, 650, 364},
		{"4:4:4", spsParams{profile: 244, chromaFormat: 3, mbWidth: 41, mbHeight: 23, frameMbsOnly: 1, cropL: 1, cropR: 2, cropT: 1, cropB: 3}, 653, 364},
		{"monochrome", spsParams{profile: 100, chromaFormat: 0, mbWidth: 41, mbHeight: 23, frameMbsOnly: 1, cropL: 1, cropR: 2, cropT: 1, cropB: 3}, 653, 364},
		{"4:4:4 separate colour planes", spsParams{profile: 244, chromaFormat: 3, separateColourPlane: 1, mbWidth: 41, mbHeight: 23, frameMbsOnly: 1, cropR: 3, cropB: 5}, 653, 363},
		// the height is given in pairs of field macroblocks
		{"4:2:0 fields", spsParams{profile: 100, chromaFormat: 1, mbWidth: 40, mbHeight: 12, frameMbsOnly: 0, cropB: 6}, 640, 360},
		{"4:4:4 fields", spsParams{profile: 244, chromaFormat: 3, mbWidth: 40, mbHeight: 12, frameMbsOnly: 0, cropR: 1, cropB: 11}, 639, 362},
		// 8 scaling lists, 12 for 4:4:4
		{"4:2:0 scaling lists", spsParams{profile: 100, chromaFormat: 1, scalingLists: 8, mbWidth: 40, mbHeight: 23, frameMbsOnly: 1, cropB: 4}, 640, 360},
		{"4:4:4 scaling lists", spsParams{profile: 244, chromaFormat: 3, scalingLists: 12, mbWidth: 40, mbHeight: 23, frameMbsOnly: 1, cropB: 8}, 640, 360},
		// profiles with chroma format (MVC, SVC)
		{"profile 128", spsParams{profile: 128, chromaFormat: 1, mbWidth: 40, mbHeight: 23, frameMbsOnly: 1, cropB: 4}, 640, 360},
		{"profile 134", spsParams{profile: 134, chromaFormat: 1, mbWidth: 40, mbHeight: 23, frameMbsOnly: 1, cropB: 4}, 640, 360},
	} {
		t.Run(tc.name, func(t *testing.T) {
			info, err := ParseSPS(buildSPS(tc.params))
			require.NoError(t, err)
			require.Equal(t, tc.width, info.Width)
			require.Equal(t, tc.height, info.Height)
		})
	}
}
