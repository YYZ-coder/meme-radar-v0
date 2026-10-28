package api

import "context"

// ProjectionRow is one inflow_rows entry: the wire row plus the attributes the
// API filters on that the wire row only carries as display names.
type ProjectionRow struct {
	Row           InflowRow
	DexIDs        []string
	Platform      string   // launch platform id, "" if unknown
	DevHoldingPct *float64 // developer holding share in percent; nil if unknown
}

// Projection is the current inflow projection for one chain filter and window.
type Projection struct {
	AsOf        string
	ChainAsOf   ChainTimes
	ListVersion string
	Rows        []ProjectionRow
	// LoadRows, when set, resolves selected keys at this immutable projection.
	// Rows then carry selection attributes only; callers must load wire payloads.
	LoadRows func(context.Context, []TokenKey) ([]InflowRow, error)
}

// InflowPages binds a capped, ordered result to immutable publication versions.
// LoadPage reuses the first transaction's page and reads later pages at the
// same versions. Callers retain metadata and enforce the query lifetime.
type InflowPages struct {
	AsOf        string
	ChainAsOf   ChainTimes
	ListVersion string
	Total       int
	Truncated   bool
	LoadPage    func(context.Context, int, int) ([]InflowRow, error)
}

// CandleQuery is a validated candles request.
type CandleQuery struct {
	Interval string // 1s, 30s, 1m, 5m, 1h or 1d
	Metric   string // price or market_cap
	Native   bool
	From, To *int64 // Unix seconds
	Limit    int
}

// ActivityQuery is a validated activities request.
type ActivityQuery struct {
	Kind     string // all, trade or liquidity
	Label    string // ActivityLabelFilter
	From, To *int64 // Unix seconds
	Cursor   string
	Limit    int
}
