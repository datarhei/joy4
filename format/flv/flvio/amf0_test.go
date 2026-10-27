package flvio

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestParseAMF0StrictArrayCountTooLarge(t *testing.T) {
	// strict array with count 0xFFFFFFFF and a single null value
	b := []byte{strictarraymarker, 0xFF, 0xFF, 0xFF, 0xFF, nullmarker}

	_, _, err := ParseAMF0Val(b)
	require.Error(t, err)
}

func TestParseAMF0StrictArrayCountExceedsData(t *testing.T) {
	// strict array with count 3 but only two values
	b := []byte{strictarraymarker, 0x00, 0x00, 0x00, 0x03, nullmarker, nullmarker}

	_, _, err := ParseAMF0Val(b)
	require.Error(t, err)
}

func TestParseAMF0StrictArray(t *testing.T) {
	val := AMFArray{"av01", "vp09", "hvc1"}

	b := make([]byte, LenAMF0Val(val))
	n := FillAMF0Val(b, val)
	require.Equal(t, len(b), n)

	parsed, size, err := ParseAMF0Val(b)
	require.NoError(t, err)
	require.Equal(t, n, size)
	require.Equal(t, val, parsed)
}

func TestParseAMF0EmptyStrictArray(t *testing.T) {
	b := []byte{strictarraymarker, 0x00, 0x00, 0x00, 0x00}

	parsed, size, err := ParseAMF0Val(b)
	require.NoError(t, err)
	require.Equal(t, 5, size)
	require.Equal(t, AMFArray{}, parsed)
}
