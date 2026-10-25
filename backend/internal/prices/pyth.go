package prices

import (
	"context"
	"fmt"
	"math/big"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Pyth reads Pyth Hermes v2 price updates (parsed=true). Not verified
// against the live API: the public endpoint answered 401 on 2026-10-23,
// so the parser follows the documented response format.
type Pyth struct {
	base   string
	c      *http.Client
	header http.Header
	feeds  map[string]string // symbol -> feed id
}

func NewPyth(baseURL string, c *http.Client, keyHeader, key string, feeds map[string]string) *Pyth {
	h := http.Header{}
	if key != "" {
		h.Set(keyHeader, key)
	}
	f := map[string]string{}
	for s, id := range feeds {
		f[s] = strings.TrimPrefix(strings.ToLower(id), "0x")
	}
	return &Pyth{base: strings.TrimRight(baseURL, "/"), c: c, header: h, feeds: f}
}

type pythUpdate struct {
	Parsed []struct {
		ID    string
		Price struct {
			Price       string
			Expo        int
			PublishTime int64 `json:"publish_time"`
		}
	}
}

// at fetches the prices of symbols at path ("latest" or a unix second).
func (p *Pyth) at(ctx context.Context, path string, symbols []string) (map[string]Price, error) {
	q := url.Values{"parsed": {"true"}}
	bySym := map[string]string{}
	for _, s := range symbols {
		id, ok := p.feeds[s]
		if !ok {
			return nil, fmt.Errorf("prices: pyth has no feed for %s", s)
		}
		q.Add("ids[]", id)
		bySym[id] = s
	}
	var u pythUpdate
	if err := getWith(ctx, p.c, p.base+"/v2/updates/price/"+path+"?"+q.Encode(), p.header, &u); err != nil {
		return nil, err
	}
	out := map[string]Price{}
	for _, r := range u.Parsed {
		s, ok := bySym[strings.TrimPrefix(strings.ToLower(r.ID), "0x")]
		if !ok {
			continue
		}
		usd, err := scaled(r.Price.Price, r.Price.Expo)
		if err != nil {
			return nil, fmt.Errorf("prices: pyth %s: %w", s, err)
		}
		out[s] = Price{Symbol: s, T: time.Unix(r.Price.PublishTime, 0).UTC(), USD: usd, Source: "pyth"}
	}
	return complete(out, symbols, "pyth")
}

func (p *Pyth) Latest(ctx context.Context, symbols []string) (map[string]Price, error) {
	return p.at(ctx, "latest", symbols)
}

// Minutes asks for each minute's price at its last second (one request per minute).
func (p *Pyth) Minutes(ctx context.Context, symbol string, from, to time.Time) ([]Price, error) {
	var out []Price
	for m := from.UTC().Truncate(time.Minute); !m.After(to); m = m.Add(time.Minute) {
		got, err := p.at(ctx, fmt.Sprint(m.Unix()+59), []string{symbol})
		if err != nil {
			return nil, err
		}
		q := got[symbol]
		q.T = m
		out = append(out, q)
	}
	return out, nil
}

// scaled renders the integer mantissa x 10^expo as a decimal string.
func scaled(mantissa string, expo int) (string, error) {
	n, ok := new(big.Int).SetString(mantissa, 10)
	if !ok {
		return "", fmt.Errorf("bad price %q", mantissa)
	}
	if expo >= 0 {
		return new(big.Int).Mul(n, new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(expo)), nil)).String(), nil
	}
	neg := n.Sign() < 0
	digits := new(big.Int).Abs(n).String()
	if pad := -expo + 1 - len(digits); pad > 0 {
		digits = strings.Repeat("0", pad) + digits
	}
	s := trimZeros(digits[:len(digits)+expo] + "." + digits[len(digits)+expo:])
	if neg {
		s = "-" + s
	}
	return s, nil
}
