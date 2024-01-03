package h264parser

import (
	"encoding/hex"
	"testing"
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
