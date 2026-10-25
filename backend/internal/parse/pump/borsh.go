package pump

import (
	"encoding/binary"
	"errors"

	"github.com/meme-radar/radar/internal/solana"
)

var errShort = errors.New("pump: event data too short")

// borsh reads little-endian Borsh fields; the first short read sets err and
// later reads return zero values. Programs append fields over time, so
// callers check ok() before reading optional trailing fields.
type borsh struct {
	b   []byte
	err error
}

func (r *borsh) take(n int) []byte {
	if r.err != nil || len(r.b) < n {
		r.err = errShort
		return nil
	}
	out := r.b[:n]
	r.b = r.b[n:]
	return out
}

func (r *borsh) u8() uint8 {
	if b := r.take(1); b != nil {
		return b[0]
	}
	return 0
}
func (r *borsh) boolean() bool { return r.u8() != 0 }
func (r *borsh) u16() uint16 {
	if b := r.take(2); b != nil {
		return binary.LittleEndian.Uint16(b)
	}
	return 0
}
func (r *borsh) u64() uint64 {
	if b := r.take(8); b != nil {
		return binary.LittleEndian.Uint64(b)
	}
	return 0
}
func (r *borsh) i64() int64 { return int64(r.u64()) }
func (r *borsh) key() solana.Key {
	var k solana.Key
	if b := r.take(32); b != nil {
		copy(k[:], b)
	}
	return k
}
func (r *borsh) str() string {
	n := r.take(4)
	if n == nil {
		return ""
	}
	return string(r.take(int(binary.LittleEndian.Uint32(n))))
}
func (r *borsh) skip(n int) { r.take(n) }

// more reports whether an optional trailing field is present.
func (r *borsh) more() bool { return r.err == nil && len(r.b) > 0 }
