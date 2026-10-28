package flvio

import (
	"bytes"
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

func TestAMF0StringLength(t *testing.T) {
	for _, tc := range []struct {
		length int
		marker byte
		header int
	}{
		{0, stringmarker, 3},
		{0xFFFF, stringmarker, 3},
		{0xFFFF + 1, longstringmarker, 5},
		{0xFFFF + 2, longstringmarker, 5},
	} {
		val := string(bytes.Repeat([]byte{'x'}, tc.length))

		b := make([]byte, LenAMF0Val(val))
		require.Equal(t, tc.header+tc.length, len(b), "length %d", tc.length)

		n := FillAMF0Val(b, val)
		require.Equal(t, len(b), n, "length %d", tc.length)
		require.Equal(t, tc.marker, b[0], "length %d", tc.length)

		parsed, size, err := ParseAMF0Val(b)
		require.NoError(t, err, "length %d", tc.length)
		require.Equal(t, n, size, "length %d", tc.length)
		require.Equal(t, val, parsed, "length %d", tc.length)
	}
}

// amf0RoundTrip encodes the value, checks the length, and returns the parsed value.
func amf0RoundTrip(t *testing.T, val interface{}) interface{} {
	b := make([]byte, LenAMF0Val(val))
	n := FillAMF0Val(b, val)
	require.Equal(t, len(b), n)

	parsed, size, err := ParseAMF0Val(b)
	require.NoError(t, err)
	require.Equal(t, n, size)

	return parsed
}

func TestAMF0GoMapAndSlice(t *testing.T) {
	val := map[string]interface{}{
		"a": 1.0,
		"b": []interface{}{"x", 2.0},
		"c": map[string]interface{}{"d": true},
	}

	require.Equal(t, AMFMap{
		"a": 1.0,
		"b": AMFArray{"x", 2.0},
		"c": AMFMap{"d": true},
	}, amf0RoundTrip(t, val))

	require.Equal(t, AMFArray{"x", AMFMap{"y": 3.0}}, amf0RoundTrip(t, []interface{}{"x", map[string]interface{}{"y": 3.0}}))
}

func TestAMF0UnsupportedType(t *testing.T) {
	val := AMFMap{
		"width":   1280.0,
		"pointer": &struct{}{},
		"func":    func() {},
		"strings": []string{"x"},
		"nil":     (*int)(nil),
		"after":   "ok",
	}

	// unsupported values become null, the other values are kept
	require.Equal(t, AMFMap{
		"width":   1280.0,
		"pointer": nil,
		"func":    nil,
		"strings": nil,
		"nil":     nil,
		"after":   "ok",
	}, amf0RoundTrip(t, val))
}

// typedObject returns an AMF0 typed object with the class name and a number property "a" with the value 1.
func typedObject(class string) []byte {
	b := []byte{typedobjectmarker, 0x00, byte(len(class))}
	b = append(b, class...)
	b = append(b, 0x00, 0x01, 'a')
	num := make([]byte, lenAMF0Number)
	fillAMF0Number(num, 1)
	b = append(b, num...)
	return append(b, 0x00, 0x00, objectendmarker)
}

func TestParseAMF0TypedObject(t *testing.T) {
	b := typedObject("Foo")

	val, n, err := ParseAMF0Val(b)
	require.NoError(t, err)
	require.Equal(t, len(b), n)
	require.Equal(t, AMFMap{"a": 1.0}, val)

	// nested in an object
	nested := []byte{objectmarker, 0x00, 0x01, 't'}
	nested = append(nested, typedObject("Bar")...)
	nested = append(nested, 0x00, 0x00, objectendmarker)

	val, n, err = ParseAMF0Val(nested)
	require.NoError(t, err)
	require.Equal(t, len(nested), n)
	require.Equal(t, AMFMap{"t": AMFMap{"a": 1.0}}, val)

	// truncated class name
	_, _, err = ParseAMF0Val([]byte{typedobjectmarker, 0x00, 0x05, 'F', 'o'})
	require.Error(t, err)
}

func TestParseAMF0Reference(t *testing.T) {
	// references are not supported
	_, _, err := ParseAMF0Val([]byte{referencemarker, 0x00, 0x01})
	require.Error(t, err)
}
