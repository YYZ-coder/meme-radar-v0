package httpapi

import (
	"net/http"
	"regexp"
	"strconv"

	"github.com/meme-radar/radar/internal/api"
)

var (
	solanaAddr = regexp.MustCompile(`^[1-9A-HJ-NP-Za-km-z]{32,44}$`)
	evmAddr    = regexp.MustCompile(`^0x[0-9a-fA-F]{40}$`)
)

// pathToken validates the {chain}/{address} path segments.
func pathToken(r *http.Request) (api.Chain, string, error) {
	chain, address := api.Chain(r.PathValue("chain")), r.PathValue("address")
	if !chain.Valid() {
		return "", "", badParam("chain", "unknown chain")
	}
	if (chain == api.Solana && !solanaAddr.MatchString(address)) || (chain != api.Solana && !evmAddr.MatchString(address)) {
		return "", "", badParam("address", "malformed address")
	}
	return chain, address, nil
}

func queryTime(r *http.Request, name string) (*int64, error) {
	raw := r.URL.Query().Get(name)
	if raw == "" {
		return nil, nil
	}
	v, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || v < 0 {
		return nil, badParam(name, name+" must be Unix seconds")
	}
	return &v, nil
}

func oneOf(r *http.Request, name, def string, allowed ...string) (string, error) {
	v := r.URL.Query().Get(name)
	if v == "" {
		return def, nil
	}
	for _, a := range allowed {
		if v == a {
			return v, nil
		}
	}
	return "", badParam(name, "unsupported "+name)
}

// tokenHandler adapts a per-token read to an http.HandlerFunc.
func (s *server) tokenHandler(read func(r *http.Request, chain api.Chain, address string) (any, error)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		chain, address, err := pathToken(r)
		if err == nil {
			var v any
			if v, err = read(r, chain, address); err == nil {
				s.json(w, http.StatusOK, v)
				return
			}
		}
		s.fail(w, r, err)
	}
}

func (s *server) tokenRoutes(mux *http.ServeMux) {
	const p = "GET /api/v1/tokens/{chain}/{address}"
	mux.HandleFunc(p, s.tokenHandler(func(r *http.Request, c api.Chain, a string) (any, error) {
		native, err := queryCurrency(r.URL.Query().Get("currency"))
		if err != nil {
			return nil, err
		}
		return s.b.TokenDetail(r.Context(), c, a, native)
	}))
	mux.HandleFunc(p+"/candles", s.tokenHandler(func(r *http.Request, c api.Chain, a string) (any, error) {
		var q api.CandleQuery
		var err error
		if q.Interval = r.URL.Query().Get("interval"); q.Interval == "" {
			return nil, badParam("interval", "interval is required")
		}
		if q.Interval, err = oneOf(r, "interval", "", "1s", "30s", "1m", "5m", "1h", "1d"); err != nil {
			return nil, err
		}
		if q.Metric, err = oneOf(r, "metric", "price", "price", "market_cap"); err != nil {
			return nil, err
		}
		if q.Native, err = queryCurrency(r.URL.Query().Get("currency")); err != nil {
			return nil, err
		}
		if q.From, err = queryTime(r, "from"); err != nil {
			return nil, err
		}
		if q.To, err = queryTime(r, "to"); err != nil {
			return nil, err
		}
		if q.Limit, err = queryInt(r, "limit", 300, 1, 1000); err != nil {
			return nil, err
		}
		return s.b.Candles(r.Context(), c, a, q)
	}))
	mux.HandleFunc(p+"/activities", s.tokenHandler(func(r *http.Request, c api.Chain, a string) (any, error) {
		q := api.ActivityQuery{Cursor: r.URL.Query().Get("cursor")}
		var err error
		if q.Kind, err = oneOf(r, "kind", "all", "all", "trade", "liquidity"); err != nil {
			return nil, err
		}
		if q.Label, err = oneOf(r, "label", "all", "all", "smart_money", "kol", "insider", "whale", "sniper", "dev", "bundler"); err != nil {
			return nil, err
		}
		if q.From, err = queryTime(r, "from"); err != nil {
			return nil, err
		}
		if q.To, err = queryTime(r, "to"); err != nil {
			return nil, err
		}
		if q.Limit, err = queryInt(r, "limit", 50, 1, 100); err != nil {
			return nil, err
		}
		return s.b.Activities(r.Context(), c, a, q)
	}))
	mux.HandleFunc(p+"/holders", s.tokenHandler(func(r *http.Request, c api.Chain, a string) (any, error) {
		label, err := oneOf(r, "label", "all", "all", "smart_money")
		if err != nil {
			return nil, err
		}
		limit, err := queryInt(r, "limit", 50, 1, 100)
		if err != nil {
			return nil, err
		}
		return s.b.Holders(r.Context(), c, a, label, r.URL.Query().Get("cursor"), limit)
	}))
	mux.HandleFunc(p+"/pools", s.tokenHandler(func(r *http.Request, c api.Chain, a string) (any, error) {
		return s.b.Pools(r.Context(), c, a)
	}))
	mux.HandleFunc(p+"/liquidity-events", s.tokenHandler(func(r *http.Request, c api.Chain, a string) (any, error) {
		limit, err := queryInt(r, "limit", 50, 1, 100)
		if err != nil {
			return nil, err
		}
		return s.b.LiquidityEvents(r.Context(), c, a, r.URL.Query().Get("cursor"), limit)
	}))
	mux.HandleFunc(p+"/smart-money", s.tokenHandler(func(r *http.Request, c api.Chain, a string) (any, error) {
		return s.b.TokenSmartMoney(r.Context(), c, a)
	}))
}
