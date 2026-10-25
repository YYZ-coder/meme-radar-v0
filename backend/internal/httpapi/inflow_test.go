package httpapi_test

import (
	"net/http"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"
)

const inflowRoute = "/api/v1/smart-money/inflow"

type inflowRow struct {
	Token struct {
		Chain, Address string
		LaunchPlatform *string `json:"launch_platform"`
	}
	Currency  string
	Liquidity *string
	SMTrades  *int64 `json:"sm_trades"`
	Dexes     []string
	Socials   struct{ Twitter *string }
}

type inflowPage struct {
	QueryID    string `json:"query_id"`
	AsOf       string `json:"as_of"`
	Total      int
	NextOffset *int `json:"next_offset"`
	Items      []inflowRow
}

// all follows next_offset from a first query and returns every row.
func (h *harness) all(query string) []inflowRow {
	h.t.Helper()
	var p inflowPage
	h.get(inflowRoute, inflowRoute+"?"+query, http.StatusOK, &p)
	rows, asOf := p.Items, p.AsOf
	for p.NextOffset != nil {
		id, next := p.QueryID, *p.NextOffset
		p = inflowPage{} // json.Unmarshal would reuse Items' backing array and overwrite collected rows
		h.get(inflowRoute, inflowRoute+"?query_id="+id+"&offset="+strconv.Itoa(next)+"&limit=50", http.StatusOK, &p)
		if p.AsOf != asOf {
			h.t.Fatalf("as_of changed across pages: %s → %s", asOf, p.AsOf)
		}
		rows = append(rows, p.Items...)
	}
	if len(rows) != p.Total || p.Total > 2000 {
		h.t.Fatalf("paged %d rows, total %d (max 2000)", len(rows), p.Total)
	}
	return rows
}

func key(r inflowRow) string { return r.Token.Chain + ":" + r.Token.Address }

// 01-api §4: pages slice one frozen snapshot — no repeats, no gaps.
func TestInflowPagingHasNoRepeats(t *testing.T) {
	h := newHarness(t)
	rows := h.all("window=24h&limit=50")
	seen := map[string]bool{}
	for _, r := range rows {
		if seen[key(r)] {
			t.Fatalf("row %s repeated", key(r))
		}
		seen[key(r)] = true
	}
	if len(rows) < 100 {
		t.Fatalf("only %d rows; want several pages", len(rows))
	}
}

// Default sort sm_trades desc; nulls always last; equal values by (chain, address) asc.
func TestInflowSortOrder(t *testing.T) {
	h := newHarness(t)
	check := func(query string, value func(inflowRow) *float64, desc bool) {
		rows := h.all(query)
		for i := 1; i < len(rows); i++ {
			a, b := value(rows[i-1]), value(rows[i])
			switch {
			case a == nil && b != nil:
				t.Fatalf("%s: null before value at %d", query, i)
			case a == nil || b == nil:
			case *a == *b && key(rows[i-1]) > key(rows[i]):
				t.Fatalf("%s: tie at %d not ordered by key: %s > %s", query, i, key(rows[i-1]), key(rows[i]))
			case desc && *a < *b, !desc && *a > *b:
				t.Fatalf("%s: out of order at %d: %v then %v", query, i, *a, *b)
			}
		}
	}
	check("window=1h", func(r inflowRow) *float64 {
		if r.SMTrades == nil {
			return nil
		}
		v := float64(*r.SMTrades)
		return &v
	}, true)
	check("window=1h&sort=liquidity&order=asc", func(r inflowRow) *float64 {
		if r.Liquidity == nil {
			return nil
		}
		v, _ := strconv.ParseFloat(*r.Liquidity, 64)
		return &v
	}, false)
}

func TestInflowFilters(t *testing.T) {
	h := newHarness(t)
	for _, r := range h.all("window=24h&min_liquidity=1000") {
		if r.Liquidity == nil {
			t.Fatalf("%s: null liquidity passed min filter", key(r))
		}
		if v, _ := strconv.ParseFloat(*r.Liquidity, 64); v < 1000 {
			t.Fatalf("%s: liquidity %v < 1000", key(r), v)
		}
	}
	base := h.all("window=24h&chain=base&dex=uniswap_v4&socials=twitter")
	if len(base) == 0 {
		t.Fatal("base uniswap_v4 twitter filter returned nothing")
	}
	for _, r := range base {
		if r.Token.Chain != "base" || !slices.Contains(r.Dexes, "Uniswap v4") || r.Socials.Twitter == nil {
			t.Fatalf("%s violates chain/dex/socials filter: %+v", key(r), r)
		}
	}
	for _, r := range h.all("window=24h&platform=pump_fun&currency=native") {
		if r.Token.LaunchPlatform == nil || *r.Token.LaunchPlatform != "Pump.fun" || r.Currency != "SOL" {
			t.Fatalf("%s: platform %v currency %s, want Pump.fun / SOL", key(r), r.Token.LaunchPlatform, r.Currency)
		}
	}
}

func TestInflowRejectsBadParams(t *testing.T) {
	h := newHarness(t)
	cases := map[string][2]string{
		"window=1h&min_volume=50&max_volume=10": {"invalid_range", "min_volume"},
		"window=1h&min_trades=abc":              {"invalid_param", "min_trades"},
		"window=1h&sort=price":                  {"invalid_param", "sort"},
		"window=1h&limit=101":                   {"invalid_param", "limit"},
		"window=7d":                             {"invalid_param", "window"},
	}
	for q, want := range cases {
		var e errorBody
		h.get(inflowRoute, inflowRoute+"?"+q, http.StatusBadRequest, &e)
		if e.Error.Code != want[0] || e.Error.Field == nil || *e.Error.Field != want[1] {
			t.Errorf("%s: error %s/%v, want %s/%s", q, e.Error.Code, e.Error.Field, want[0], want[1])
		}
	}
}

// Unknown or expired (TTL 10 minutes) query_id → 410 query_expired.
func TestInflowSnapshotExpiry(t *testing.T) {
	h := newHarness(t)
	var e errorBody
	h.get(inflowRoute, inflowRoute+"?query_id=nope&offset=50", http.StatusGone, &e)
	if e.Error.Code != "query_expired" {
		t.Errorf("code = %s, want query_expired", e.Error.Code)
	}
	var p inflowPage
	h.get(inflowRoute, inflowRoute+"?window=1h", http.StatusOK, &p)
	h.clock.Advance(9 * time.Minute)
	h.get(inflowRoute, inflowRoute+"?query_id="+p.QueryID+"&offset=50", http.StatusOK, nil)
	h.clock.Advance(10*time.Minute + time.Second)
	h.get(inflowRoute, inflowRoute+"?query_id="+p.QueryID+"&offset=50", http.StatusGone, nil)
	if strings.TrimSpace(p.QueryID) == "" {
		t.Error("empty query_id")
	}
}
