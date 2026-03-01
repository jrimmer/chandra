package store

import (
	"encoding/binary"
	"math"
)

// SerializeFloat32 converts a []float32 slice to little-endian bytes
// in the format expected by sqlite-vec for FLOAT[] columns.
func SerializeFloat32(v []float32) []byte {
	buf := make([]byte, len(v)*4)
	for i, f := range v {
		binary.LittleEndian.PutUint32(buf[i*4:], math.Float32bits(f))
	}
	return buf
}
