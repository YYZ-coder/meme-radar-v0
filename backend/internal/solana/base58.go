package solana

import (
	"errors"
	"fmt"
	"math/big"
)

const alphabet = "123456789ABCDEFGHJKLMNPQRSTUVWXYZabcdefghijkmnopqrstuvwxyz"

var big58 = big.NewInt(58)

// EncodeBase58 renders b in the Bitcoin alphabet used for Solana keys and
// signatures; each leading zero byte becomes '1'.
func EncodeBase58(b []byte) string {
	zeros := 0
	for zeros < len(b) && b[zeros] == 0 {
		zeros++
	}
	n := new(big.Int).SetBytes(b)
	var out []byte
	mod := new(big.Int)
	for n.Sign() > 0 {
		n.DivMod(n, big58, mod)
		out = append(out, alphabet[mod.Int64()])
	}
	for range zeros {
		out = append(out, '1')
	}
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return string(out)
}

// DecodeBase58 is the inverse of EncodeBase58.
func DecodeBase58(s string) ([]byte, error) {
	if s == "" {
		return nil, errors.New("solana: empty base58 string")
	}
	n := new(big.Int)
	zeros := 0
	for i := 0; i < len(s); i++ {
		d := index58[s[i]]
		if d < 0 {
			return nil, fmt.Errorf("solana: invalid base58 character %q", s[i])
		}
		if d == 0 && n.Sign() == 0 {
			zeros++
		}
		n.Mul(n, big58).Add(n, big.NewInt(int64(d)))
	}
	return append(make([]byte, zeros), n.Bytes()...), nil
}

var index58 = func() (t [256]int8) {
	for i := range t {
		t[i] = -1
	}
	for i := 0; i < len(alphabet); i++ {
		t[alphabet[i]] = int8(i)
	}
	return t
}()

// ParseKey decodes a base58 address.
func ParseKey(s string) (Key, error) {
	b, err := DecodeBase58(s)
	if err != nil {
		return Key{}, err
	}
	if len(b) != 32 {
		return Key{}, fmt.Errorf("solana: key %q is %d bytes, want 32", s, len(b))
	}
	return Key(b), nil
}

// MustKey is ParseKey for constants.
func MustKey(s string) Key {
	k, err := ParseKey(s)
	if err != nil {
		panic(err)
	}
	return k
}

// String renders the key in base58.
func (k Key) String() string { return EncodeBase58(k[:]) }
