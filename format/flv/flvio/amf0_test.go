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

// nestedAMF0Objects returns depth objects, each nested in the previous one under the key "a".
func nestedAMF0Objects(depth int) []byte {
	b := []byte{}
	for i := 0; i < depth; i++ {
		b = append(b, objectmarker)
		if i < depth-1 {
			b = append(b, 0x00, 0x01, 'a')
		}
	}
	for i := 0; i < depth; i++ {
		b = append(b, 0x00, 0x00, objectendmarker)
	}
	return b
}

func TestParseAMF0MaxDepth(t *testing.T) {
	// top level object plus MaxAMF0Depth nested objects
	b := nestedAMF0Objects(MaxAMF0Depth + 1)

	val, n, err := ParseAMF0Val(b)
	require.NoError(t, err)
	require.Equal(t, len(b), n)

	for i := 0; i < MaxAMF0Depth; i++ {
		obj, ok := val.(AMFMap)
		require.True(t, ok)
		val = obj["a"]
	}
	require.Equal(t, AMFMap{}, val)
}

func TestParseAMF0DepthExceeded(t *testing.T) {
	b := nestedAMF0Objects(MaxAMF0Depth + 2)

	_, _, err := ParseAMF0Val(b)
	require.ErrorContains(t, err, "depth:")
}

func TestParseAMF0DeepNesting(t *testing.T) {
	// a 16 MB message full of nested objects must not exhaust the stack
	b := make([]byte, 0, 0xFFFFFF)
	for len(b)+4 <= cap(b) {
		b = append(b, objectmarker, 0x00, 0x01, 'a')
	}

	_, _, err := ParseAMF0Val(b)
	require.ErrorContains(t, err, "depth:")

	// the same with strict arrays and ECMA arrays
	b = b[:0]
	for len(b)+5 <= cap(b) {
		b = append(b, strictarraymarker, 0x00, 0x00, 0x00, 0x01)
	}

	_, _, err = ParseAMF0Val(b)
	require.ErrorContains(t, err, "depth:")

	b = b[:0]
	for len(b)+8 <= cap(b) {
		b = append(b, ecmaarraymarker, 0x00, 0x00, 0x00, 0x01, 0x00, 0x01, 'a')
	}

	_, _, err = ParseAMF0Val(b)
	require.ErrorContains(t, err, "depth:")
}
