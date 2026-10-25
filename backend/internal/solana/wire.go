package solana

import (
	"encoding/binary"
	"errors"
	"fmt"
)

// message is the part of a wire transaction the decoder needs.
type message struct {
	signature    [64]byte
	numSigners   int
	keys         []Key
	instructions []wireInstruction
}

type wireInstruction struct {
	program  int
	accounts []byte
	data     []byte
}

// v1Version marks the SIMD-0385 v1 format; legacy and v0 start with the
// signature count instead.
const v1Version = 0x81

var errShort = errors.New("solana: transaction truncated")

type reader struct {
	b   []byte
	i   int
	err error
}

func (r *reader) take(n int) []byte {
	if r.err != nil || n < 0 || r.i+n > len(r.b) {
		r.err = errShort
		return nil
	}
	out := r.b[r.i : r.i+n]
	r.i += n
	return out
}

func (r *reader) u8() int {
	b := r.take(1)
	if b == nil {
		return 0
	}
	return int(b[0])
}

// shortvec reads a compact-u16 length.
func (r *reader) shortvec() int {
	v, shift := 0, 0
	for range 3 {
		c := r.u8()
		v |= (c & 0x7f) << shift
		if c < 0x80 {
			return v
		}
		shift += 7
	}
	r.err = errors.New("solana: bad compact-u16")
	return 0
}

func (r *reader) keys(n int) []Key {
	b := r.take(32 * n)
	out := make([]Key, 0, n)
	for i := 0; i+32 <= len(b); i += 32 {
		out = append(out, Key(b[i:i+32]))
	}
	return out
}

// parseWire decodes legacy, v0 and v1 transactions.
func parseWire(raw []byte) (message, error) {
	if len(raw) > 0 && raw[0] == v1Version {
		return parseV1(raw)
	}
	var m message
	r := &reader{b: raw}
	nsig := r.shortvec()
	sigs := r.take(64 * nsig)
	if nsig > 0 && sigs != nil {
		copy(m.signature[:], sigs[:64])
	}
	if r.i < len(raw) && raw[r.i]&0x80 != 0 {
		r.u8() // v0 version prefix
	}
	m.numSigners = r.u8()
	r.take(2) // readonly signed / unsigned counts
	m.keys = r.keys(r.shortvec())
	r.take(32) // recent blockhash
	n := r.shortvec()
	for range n {
		var ix wireInstruction
		ix.program = r.u8()
		ix.accounts = r.take(r.shortvec())
		ix.data = r.take(r.shortvec())
		m.instructions = append(m.instructions, ix)
	}
	// v0 address table lookups follow; their keys come from meta.loadedAddresses.
	if r.err != nil {
		return message{}, r.err
	}
	return m, nil
}

// parseV1 follows SIMD-0385: header, config mask and values, addresses,
// instruction headers then payloads, and the signatures last.
func parseV1(raw []byte) (message, error) {
	var m message
	r := &reader{b: raw}
	r.u8()
	m.numSigners = r.u8()
	r.take(2)
	maskBytes := r.take(4)
	r.take(32) // lifetime specifier
	nix := r.u8()
	m.keys = r.keys(r.u8())
	if maskBytes != nil {
		mask := binary.LittleEndian.Uint32(maskBytes)
		n := 0
		for ; mask != 0; mask &= mask - 1 {
			n++
		}
		r.take(4 * n)
	}
	type header struct{ program, accounts, data int }
	hdrs := make([]header, nix)
	for i := range hdrs {
		h := r.take(4)
		if h == nil {
			break
		}
		hdrs[i] = header{int(h[0]), int(h[1]), int(binary.LittleEndian.Uint16(h[2:]))}
	}
	for _, h := range hdrs {
		m.instructions = append(m.instructions, wireInstruction{program: h.program, accounts: r.take(h.accounts), data: r.take(h.data)})
	}
	if r.err != nil {
		return message{}, r.err
	}
	if m.numSigners < 1 || len(raw)-r.i != 64*m.numSigners {
		return message{}, fmt.Errorf("solana: v1 transaction has %d trailing bytes for %d signatures", len(raw)-r.i, m.numSigners)
	}
	copy(m.signature[:], raw[r.i:r.i+64])
	return m, nil
}
