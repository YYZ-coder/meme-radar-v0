package pgapi

import (
	"context"
	"encoding/json"
	"math/big"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"time"

	"golang.org/x/time/rate"

	"github.com/meme-radar/radar/internal/api"
	"github.com/meme-radar/radar/internal/config"
	"github.com/meme-radar/radar/internal/ingest/jsonrpc"
)

var nativeQuantity = regexp.MustCompile(`^0x[0-9a-fA-F]{1,64}$`)

type nativeBalanceKey struct {
	chain   api.Chain
	address string
}
type nativeBalanceEntry struct {
	value   string
	expires time.Time
}

// Each Backend shares clients/rate limits across requests. Cache locks never
// span network calls; failed lookups remain NULL and are not cached as zero.
type nativeBalances struct {
	mu      sync.Mutex
	cache   map[nativeBalanceKey]nativeBalanceEntry
	clients map[api.Chain]*jsonrpc.Client
}

func newNativeBalances(cfg config.Config) *nativeBalances {
	n := &nativeBalances{cache: map[nativeBalanceKey]nativeBalanceEntry{}, clients: map[api.Chain]*jsonrpc.Client{}}
	for _, ch := range cfg.Chains {
		endpoint := ch.LookupRPCURL
		if endpoint == "" {
			endpoint = ch.Ingest.RPCURL
		}
		if endpoint == "" {
			continue
		}
		rps := ch.LookupRPS
		if rps <= 0 {
			rps = 5
		}
		n.clients[api.Chain(ch.Name)] = jsonrpc.New(endpoint, &http.Client{Timeout: 30 * time.Second}, rate.NewLimiter(rate.Limit(rps), 1), nil)
	}
	return n
}

func (n *nativeBalances) page(ctx context.Context, chain api.Chain, addresses []string, now time.Time) map[string]*string {
	out := make(map[string]*string, len(addresses))
	missing := make([]string, 0, len(addresses))
	n.mu.Lock()
	for key, e := range n.cache {
		if !now.Before(e.expires) {
			delete(n.cache, key)
		}
	}
	for _, address := range addresses {
		if e, ok := n.cache[nativeBalanceKey{chain, address}]; ok {
			value := e.value
			out[address] = &value
		} else {
			missing = append(missing, address)
		}
	}
	n.mu.Unlock()
	rpc := n.clients[chain]
	if rpc == nil || len(missing) == 0 {
		return out
	}
	if chain == api.Solana {
		raw, err := rpc.Call(ctx, "getMultipleAccounts", []any{missing, map[string]string{"encoding": "base64", "commitment": "confirmed"}})
		if err != nil {
			return out
		}
		var result struct {
			Value []*struct {
				Lamports *uint64 `json:"lamports"`
			} `json:"value"`
		}
		if json.Unmarshal(raw, &result) != nil || len(result.Value) != len(missing) {
			return out
		}
		for i, account := range result.Value {
			if account == nil || account.Lamports == nil {
				continue
			}
			value := wholeNative(new(big.Int).SetUint64(*account.Lamports), 9)
			out[missing[i]] = &value
			n.remember(chain, missing[i], value, now)
		}
		return out
	}
	for _, address := range missing {
		if ctx.Err() != nil {
			break
		}
		raw, err := rpc.Call(ctx, "eth_getBalance", []string{address, "latest"})
		if err != nil {
			continue
		}
		var hex string
		if json.Unmarshal(raw, &hex) != nil || !nativeQuantity.MatchString(hex) {
			continue
		}
		amount, ok := new(big.Int).SetString(hex[2:], 16)
		if !ok || amount.Sign() < 0 {
			continue
		}
		value := wholeNative(amount, 18)
		out[address] = &value
		n.remember(chain, address, value, now)
	}
	return out
}

func (n *nativeBalances) remember(chain api.Chain, address, value string, now time.Time) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.cache[nativeBalanceKey{chain, address}] = nativeBalanceEntry{value, now.Add(time.Minute)}
}

func wholeNative(amount *big.Int, decimals int) string {
	s := amount.String()
	if len(s) <= decimals {
		s = strings.Repeat("0", decimals+1-len(s)) + s
	}
	s = s[:len(s)-decimals] + "." + s[len(s)-decimals:]
	return strings.TrimRight(strings.TrimRight(s, "0"), ".")
}
