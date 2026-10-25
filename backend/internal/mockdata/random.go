package mockdata

import (
	"math"
	"strconv"
	"strings"
	"unicode/utf16"
)

// hash is FNV-1a 32-bit over UTF-16 code units, matching frontend/src/lib/hash.ts.
func hash(text string) uint32 {
	h := uint32(2166136261)
	for _, u := range utf16.Encode([]rune(text)) {
		h ^= uint32(u)
		h *= 16777619
	}
	return h
}

// rng is mulberry32, matching frontend/src/mock/random.ts.
func rng(seed uint32) func() float64 {
	a := seed
	return func() float64 {
		a += 0x6d2b79f5
		t := a
		t = (t ^ t>>15) * (t | 1)
		t ^= t + (t^t>>7)*(t|61)
		return float64(t^t>>14) / 4294967296
	}
}

// unit is a uniform [0,1) value for key without generator state.
func unit(key string) float64 { return rng(hash(key))() }

const base58 = "123456789ABCDEFGHJKLMNPQRSTUVWXYZabcdefghijkmnopqrstuvwxyz"

func solanaAddress(key, suffix string) string {
	r := rng(hash("sol:" + key))
	var b strings.Builder
	for i := 0; i < 44-len(suffix); i++ {
		b.WriteByte(base58[int(r()*58)])
	}
	return b.String() + suffix
}

func evmAddress(key, suffix string) string {
	r := rng(hash("evm:" + key))
	var b strings.Builder
	b.WriteString("0x")
	for i := 0; i < 40-len(suffix); i++ {
		b.WriteByte("0123456789abcdef"[int(r()*16)])
	}
	return b.String() + suffix
}

func txHash(chain string, key string) string {
	if chain == "solana" {
		return solanaAddress("tx:"+key, "") + solanaAddress("tx2:"+key, "")
	}
	r := rng(hash("txh:" + key))
	var b strings.Builder
	b.WriteString("0x")
	for i := 0; i < 64; i++ {
		b.WriteByte("0123456789abcdef"[int(r()*16)])
	}
	return b.String()
}

// jsRound is JavaScript Math.round (halves round toward +Inf).
func jsRound(x float64) float64 { return math.Floor(x + 0.5) }

// num formats x like JavaScript String(number) for the magnitudes used in keys.
func num(x float64) string { return strconv.FormatFloat(x, 'f', -1, 64) }

// dec renders value with up to digits significant digits and no exponent.
func dec(value float64, digits int) string {
	if math.IsNaN(value) || math.IsInf(value, 0) || value == 0 {
		return "0"
	}
	abs := math.Abs(value)
	magnitude := int(math.Floor(math.Log10(abs)))
	decimals := max(0, min(20, digits-1-magnitude))
	text := strconv.FormatFloat(abs, 'f', decimals, 64)
	if strings.Contains(text, ".") {
		text = strings.TrimRight(strings.TrimRight(text, "0"), ".")
	}
	if text == "0" {
		return "0"
	}
	if value < 0 {
		return "-" + text
	}
	return text
}

// decp is dec for nullable values.
func decp(value *float64, digits int) *string {
	if value == nil {
		return nil
	}
	s := dec(*value, digits)
	return &s
}

func ptr[T any](v T) *T { return &v }
