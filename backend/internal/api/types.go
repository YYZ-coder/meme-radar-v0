// Package api holds the wire types of api/openapi.yaml (v2.0.0).
//
// Nullable contract fields are pointers; a nil pointer is encoded as JSON null,
// which the frontend renders as "--". Amounts are decimal strings.
package api

// Chain identifies a supported chain.
type Chain string

// Supported chains.
const (
	Solana Chain = "solana"
	Base   Chain = "base"
	BSC    Chain = "bsc"
)

// Chains lists the supported chains in display order.
var Chains = []Chain{Solana, Base, BSC}

// Valid reports whether c is a supported chain.
func (c Chain) Valid() bool { return c == Solana || c == Base || c == BSC }

// Error is an API failure carrying the HTTP status and contract error code.
type Error struct {
	Status  int
	Code    string
	Message string
	Field   string
}

func (e *Error) Error() string { return e.Code + ": " + e.Message }

// Errorf builds an *Error.
func Errorf(status int, code, field, message string) *Error {
	return &Error{Status: status, Code: code, Message: message, Field: field}
}

// ErrorBody is the ErrorResponse envelope.
type ErrorBody struct {
	Error ErrorDetail `json:"error"`
}

// ErrorDetail is the error object inside ErrorBody.
type ErrorDetail struct {
	Code    string  `json:"code"`
	Message string  `json:"message"`
	Field   *string `json:"field"`
	TraceID string  `json:"trace_id"`
}

// Health is HealthResponse.
type Health struct {
	Status string `json:"status"`
	DB     string `json:"db,omitempty"`
}

// NativePrice is one NativePricesResponse item.
type NativePrice struct {
	Chain        Chain   `json:"chain"`
	Symbol       string  `json:"symbol"`
	PriceUSD     *string `json:"price_usd"`
	Change24hPct *string `json:"change_24h_pct"`
	Stale        bool    `json:"stale"`
	AsOf         string  `json:"as_of"`
}

// NativePrices is NativePricesResponse.
type NativePrices struct {
	Items []NativePrice `json:"items"`
}

// Socials is the Socials object; absent links are omitted.
type Socials struct {
	Twitter  *string `json:"twitter,omitempty"`
	Telegram *string `json:"telegram,omitempty"`
	Website  *string `json:"website,omitempty"`
	Github   *string `json:"github,omitempty"`
}

// TokenRef identifies and names a token.
type TokenRef struct {
	Chain          Chain   `json:"chain"`
	Address        string  `json:"address"`
	Symbol         string  `json:"symbol"`
	Name           string  `json:"name"`
	ImageURL       *string `json:"image_url"`
	LaunchPlatform *string `json:"launch_platform"`
	LaunchAt       *string `json:"launch_at"`
}

// SearchToken is a TokenRef with current market fields.
type SearchToken struct {
	TokenRef
	MarketCapUSD *string `json:"market_cap_usd"`
	AgeSeconds   *int64  `json:"age_seconds"`
	LastTradeAt  *string `json:"last_trade_at"`
}

// SearchWallet is one wallet search hit.
type SearchWallet struct {
	Chain        Chain   `json:"chain"`
	Address      string  `json:"address"`
	Label        *string `json:"label"`
	IsSmartMoney bool    `json:"is_smart_money"`
}

// Search is SearchResponse.
type Search struct {
	Tokens  []SearchToken  `json:"tokens"`
	Wallets []SearchWallet `json:"wallets"`
}

// Overview is OverviewResponse.
type Overview struct {
	AsOf               string    `json:"as_of"`
	ListVersion        string    `json:"list_version"`
	Window             string    `json:"window"`
	Coverage           string    `json:"coverage"`
	NetInflowUSD       *string   `json:"net_inflow_usd"`
	NetInflowChangePct *string   `json:"net_inflow_change_pct"`
	ActiveWallets      *int64    `json:"active_wallets"`
	TrackedTokens      *int64    `json:"tracked_tokens"`
	NewTokens          *int64    `json:"new_tokens"`
	BuyVolumeUSD       *string   `json:"buy_volume_usd"`
	SellVolumeUSD      *string   `json:"sell_volume_usd"`
	BuySharePct        *string   `json:"buy_share_pct"`
	LargeBuyVolumeUSD  *string   `json:"large_buy_volume_usd"`
	SmallBuyVolumeUSD  *string   `json:"small_buy_volume_usd"`
	LargeBuySharePct   *string   `json:"large_buy_share_pct"`
	SmallBuySharePct   *string   `json:"small_buy_share_pct"`
	InflowBars         []*string `json:"inflow_bars"`
}

// Facet is one DEX or launch platform option.
type Facet struct {
	ID                 string  `json:"id"`
	Name               string  `json:"name"`
	Chain              Chain   `json:"chain,omitempty"`
	VolumeUSD          *string `json:"volume_usd"`
	TokenCount         *int64  `json:"token_count"`
	ObservedTokenCount int64   `json:"observed_token_count"`
}

// Facets is FacetsResponse.
type Facets struct {
	Coverage  string  `json:"coverage"`
	Dexes     []Facet `json:"dexes"`
	Platforms []Facet `json:"platforms"`
}

// Observed holds lower-bound counts seen while a window is not fully covered.
type Observed struct {
	Trades int64 `json:"trades"`
	Buys   int64 `json:"buys"`
	Sells  int64 `json:"sells"`
}

// InflowRow is one inflow list row.
type InflowRow struct {
	Token          TokenRef  `json:"token"`
	Currency       string    `json:"currency"`
	Price          *string   `json:"price"`
	MarketCap      *string   `json:"market_cap"`
	PriceChangePct *string   `json:"price_change_pct"`
	Liquidity      *string   `json:"liquidity"`
	Trades         *int64    `json:"trades"`
	Buys           *int64    `json:"buys"`
	Sells          *int64    `json:"sells"`
	Observed       Observed  `json:"observed"`
	Coverage       string    `json:"coverage"`
	Volume         *string   `json:"volume"`
	NetInflow      *string   `json:"net_inflow"`
	SMHolders      *int64    `json:"sm_holders"`
	SMTrades       *int64    `json:"sm_trades"`
	Holders        *int64    `json:"holders"`
	AgeSeconds     *int64    `json:"age_seconds"`
	Dexes          []string  `json:"dexes"`
	Socials        Socials   `json:"socials"`
	Sparkline      []*string `json:"sparkline"`
}

// ChainTimes holds per-chain projection cut-offs.
type ChainTimes map[Chain]string

// InflowPage is one page of a frozen inflow snapshot.
type InflowPage struct {
	QueryID     string      `json:"query_id"`
	ListVersion string      `json:"list_version"`
	AsOf        string      `json:"as_of"`
	ChainAsOf   ChainTimes  `json:"chain_as_of,omitempty"`
	Window      string      `json:"window"`
	Total       int64       `json:"total"`
	Truncated   bool        `json:"truncated"`
	Offset      int64       `json:"offset"`
	NextOffset  *int64      `json:"next_offset"`
	Items       []InflowRow `json:"items"`
}

// TokenKey identifies a token in a batch request.
type TokenKey struct {
	Chain   Chain  `json:"chain"`
	Address string `json:"address"`
}

// BatchRequest is the visible-row refresh request.
type BatchRequest struct {
	Window      string     `json:"window"`
	Currency    string     `json:"currency"`
	QueryID     string     `json:"query_id"`
	ListVersion string     `json:"list_version"`
	Tokens      []TokenKey `json:"tokens"`
}

// Batch is BatchResponse.
type Batch struct {
	AsOf        string      `json:"as_of"`
	ChainAsOf   ChainTimes  `json:"chain_as_of,omitempty"`
	ListVersion string      `json:"list_version"`
	Items       []InflowRow `json:"items"`
}

// WindowSummary is one token detail window tab.
type WindowSummary struct {
	Window         string  `json:"window"`
	Volume         *string `json:"volume"`
	NetInflow      *string `json:"net_inflow"`
	Traders        *int64  `json:"traders"`
	Buys           *int64  `json:"buys"`
	Sells          *int64  `json:"sells"`
	BuyVolume      *string `json:"buy_volume"`
	SellVolume     *string `json:"sell_volume"`
	PriceChangePct *string `json:"price_change_pct"`
}

// Coverage is HolderCoverage.
type Coverage struct {
	Status string  `json:"status"`
	Since  *string `json:"since"`
	AsOf   string  `json:"as_of"`
}

// Creator is the token developer.
type Creator struct {
	Address    string  `json:"address"`
	HoldingPct *string `json:"holding_pct"`
}

// PoolRef is the main pool summary.
type PoolRef struct {
	Address     string  `json:"address"`
	Dex         string  `json:"dex"`
	QuoteSymbol *string `json:"quote_symbol"`
}

// TokenDetail is the token page header and side bar.
type TokenDetail struct {
	Token             TokenRef        `json:"token"`
	Currency          string          `json:"currency"`
	AsOf              string          `json:"as_of"`
	Decimals          int64           `json:"decimals"`
	TotalSupply       *string         `json:"total_supply"`
	Socials           Socials         `json:"socials"`
	Creator           *Creator        `json:"creator"`
	MainPool          *PoolRef        `json:"main_pool"`
	Price             *string         `json:"price"`
	MarketCap         *string         `json:"market_cap"`
	Liquidity         *string         `json:"liquidity"`
	Volume24h         *string         `json:"volume_24h"`
	PriceChange24hPct *string         `json:"price_change_24h_pct"`
	Holders           *int64          `json:"holders"`
	SMHolders         *int64          `json:"sm_holders"`
	ATHMarketCap      *string         `json:"ath_market_cap"`
	ATHSince          *string         `json:"ath_since"`
	HolderCoverage    Coverage        `json:"holder_coverage"`
	Windows           []WindowSummary `json:"windows"`
	SameName          []SearchToken   `json:"same_name"`
}

// Candle is one closed OHLCV bar.
type Candle struct {
	T int64  `json:"t"`
	O string `json:"o"`
	H string `json:"h"`
	L string `json:"l"`
	C string `json:"c"`
	V string `json:"v"`
}

// ObservedCandle is a bar from a partially covered or still-open interval.
type ObservedCandle struct {
	T      int64   `json:"t"`
	O      string  `json:"o"`
	H      string  `json:"h"`
	L      string  `json:"l"`
	C      string  `json:"c"`
	V      *string `json:"v"`
	Closed bool    `json:"closed"`
}

// CandleGap is a span without a USD price.
type CandleGap struct {
	From   int64  `json:"from"`
	To     int64  `json:"to"`
	Reason string `json:"reason"`
}

// PoolSwitch marks a main-pool change on the chart.
type PoolSwitch struct {
	T        int64   `json:"t"`
	FromPool *string `json:"from_pool"`
	ToPool   string  `json:"to_pool"`
	Reason   string  `json:"reason"`
}

// Candles is CandlesResponse.
type Candles struct {
	Interval      string           `json:"interval"`
	Metric        string           `json:"metric"`
	Currency      string           `json:"currency"`
	AsOf          string           `json:"as_of"`
	Coverage      string           `json:"coverage"`
	EmptyReason   *string          `json:"empty_reason"`
	Items         []Candle         `json:"items"`
	ObservedItems []ObservedCandle `json:"observed_items"`
	Gaps          []CandleGap      `json:"gaps"`
	PoolSwitches  []PoolSwitch     `json:"pool_switches"`
}

// WalletRef is a wallet with its labels.
type WalletRef struct {
	Address     string   `json:"address"`
	Labels      []string `json:"labels"`
	DisplayName *string  `json:"display_name"`
}

// Activity is one trade or liquidity change in the activity tab.
type Activity struct {
	ID           string     `json:"id"`
	Time         string     `json:"time"`
	Kind         string     `json:"kind"`
	Wallet       *WalletRef `json:"wallet"`
	Attribution  *string    `json:"attribution"`
	TokenAmount  string     `json:"token_amount"`
	QuoteAmount  *string    `json:"quote_amount"`
	QuoteSymbol  *string    `json:"quote_symbol"`
	ValueUSD     *string    `json:"value_usd"`
	PriceUSD     *string    `json:"price_usd"`
	MarketCapUSD *string    `json:"market_cap_usd"`
	TxHash       string     `json:"tx_hash"`
	Pool         string     `json:"pool"`
	Dex          string     `json:"dex"`
}

// Activities is ActivitiesResponse.
type Activities struct {
	Coverage            string            `json:"coverage"`
	LabelCountsCoverage string            `json:"label_counts_coverage"`
	Items               []Activity        `json:"items"`
	NextCursor          *string           `json:"next_cursor"`
	LabelCounts         map[string]*int64 `json:"label_counts"`
}

// Holder is one holders tab row.
type Holder struct {
	Rank             int64     `json:"rank"`
	Wallet           WalletRef `json:"wallet"`
	Amount           string    `json:"amount"`
	SharePct         *string   `json:"share_pct"`
	NativeBalance    *string   `json:"native_balance"`
	AvgBuyPriceUSD   *string   `json:"avg_buy_price_usd"`
	AvgSellPriceUSD  *string   `json:"avg_sell_price_usd"`
	RealizedPnLUSD   *string   `json:"realized_pnl_usd"`
	UnrealizedPnLUSD *string   `json:"unrealized_pnl_usd"`
}

// Holders is HoldersResponse.
type Holders struct {
	Coverage   Coverage `json:"coverage"`
	Total      *int64   `json:"total"`
	Items      []Holder `json:"items"`
	NextCursor *string  `json:"next_cursor"`
}

// Pool is one pools tab row.
type Pool struct {
	Address      string  `json:"address"`
	Dex          string  `json:"dex"`
	Protocol     string  `json:"protocol"`
	QuoteSymbol  *string `json:"quote_symbol"`
	LiquidityUSD *string `json:"liquidity_usd"`
	ReserveToken *string `json:"reserve_token"`
	ReserveQuote *string `json:"reserve_quote"`
	CreatedAt    *string `json:"created_at"`
	Status       string  `json:"status"`
	IsMain       bool    `json:"is_main"`
}

// Pools is PoolsResponse.
type Pools struct {
	Items []Pool `json:"items"`
}

// LiquidityEvent is one liquidity change.
type LiquidityEvent struct {
	ID          string     `json:"id"`
	Time        string     `json:"time"`
	Kind        string     `json:"kind"`
	TokenAmount string     `json:"token_amount"`
	QuoteAmount *string    `json:"quote_amount"`
	QuoteSymbol *string    `json:"quote_symbol"`
	ValueUSD    *string    `json:"value_usd"`
	Wallet      *WalletRef `json:"wallet"`
	TxHash      string     `json:"tx_hash"`
	Pool        string     `json:"pool"`
	Dex         string     `json:"dex"`
}

// LiquidityEvents is LiquidityEventsResponse.
type LiquidityEvents struct {
	Coverage   string           `json:"coverage"`
	Items      []LiquidityEvent `json:"items"`
	NextCursor *string          `json:"next_cursor"`
}

// TokenSmartMoneyEntry is one smart-money holder of a token.
type TokenSmartMoneyEntry struct {
	Wallet        WalletRef `json:"wallet"`
	Balance       *string   `json:"balance"`
	SharePct      *string   `json:"share_pct"`
	BuyVolumeUSD  *string   `json:"buy_volume_usd"`
	SellVolumeUSD *string   `json:"sell_volume_usd"`
	LastTradeAt   *string   `json:"last_trade_at"`
}

// TokenSmartMoney is TokenSmartMoneyResponse.
type TokenSmartMoney struct {
	ListVersion string                 `json:"list_version"`
	Holders     *int64                 `json:"holders"`
	Items       []TokenSmartMoneyEntry `json:"items"`
}

// ImportRow is one validated row of an uploaded list.
type ImportRow struct {
	Line    int64    `json:"line"`
	Chain   *string  `json:"chain"`
	Address *string  `json:"address"`
	Label   *string  `json:"label"`
	Tags    []string `json:"tags"`
	Twitter *string  `json:"twitter"`
	Source  *string  `json:"source"`
	Status  string   `json:"status"`
	Error   *string  `json:"error"`
}

// ImportTotals counts rows by status.
type ImportTotals struct {
	Rows      int64 `json:"rows"`
	Valid     int64 `json:"valid"`
	Invalid   int64 `json:"invalid"`
	Duplicate int64 `json:"duplicate"`
	Existing  int64 `json:"existing"`
}

// ImportReport is the import preview.
type ImportReport struct {
	ID                 string       `json:"id"`
	FileName           string       `json:"file_name"`
	CreatedAt          string       `json:"created_at"`
	ExpiresAt          string       `json:"expires_at"`
	Applied            bool         `json:"applied"`
	AppliedListVersion *string      `json:"applied_list_version"`
	RowsArchived       bool         `json:"rows_archived"`
	Totals             ImportTotals `json:"totals"`
	Rows               []ImportRow  `json:"rows"`
}

// ChangeCounts counts the members a list change touches.
type ChangeCounts struct {
	Added        int64 `json:"added"`
	Merged       int64 `json:"merged"`
	Removed      int64 `json:"removed"`
	TotalMembers int64 `json:"total_members"`
}

// ListVersionChange is returned when a list change is accepted.
type ListVersionChange struct {
	ListVersion     string `json:"list_version"`
	PreviousVersion string `json:"previous_version"`
	ActiveVersion   string `json:"active_version"`
	Status          string `json:"status"`
	BuildStatus     string `json:"build_status"`
	ChangeCounts
}

// ListBuildStatus reports the asynchronous preparation of a list version.
type ListBuildStatus struct {
	ListVersion     string       `json:"list_version"`
	PreviousVersion string       `json:"previous_version"`
	ActiveVersion   string       `json:"active_version"`
	Status          string       `json:"status"`
	BuildStatus     string       `json:"build_status"`
	BlockingVersion *string      `json:"blocking_version"`
	CreatedAt       string       `json:"created_at"`
	ActivatedAt     *string      `json:"activated_at"`
	ErrorCode       *string      `json:"error_code"`
	ChangeCounts    ChangeCounts `json:"change_counts"`
}

// Member is one smart-money list member.
type Member struct {
	Chain    Chain    `json:"chain"`
	Address  string   `json:"address"`
	Label    *string  `json:"label"`
	Tags     []string `json:"tags"`
	Twitter  *string  `json:"twitter"`
	Sources  []string `json:"sources"`
	AddedAt  string   `json:"added_at"`
	Excluded bool     `json:"excluded"`
}

// Members is MembersResponse.
type Members struct {
	ListVersion string   `json:"list_version"`
	Total       int64    `json:"total"`
	Items       []Member `json:"items"`
	NextCursor  *string  `json:"next_cursor"`
}

// Exclusion is one excluded wallet.
type Exclusion struct {
	Chain     Chain  `json:"chain"`
	Address   string `json:"address"`
	Reason    string `json:"reason"`
	CreatedAt string `json:"created_at"`
}

// ExclusionRequest is the body of an exclusion.
type ExclusionRequest struct {
	Chain   Chain  `json:"chain"`
	Address string `json:"address"`
	Reason  string `json:"reason"`
}

// Exclusions is ExclusionsResponse.
type Exclusions struct {
	ListVersion string      `json:"list_version"`
	Items       []Exclusion `json:"items"`
	NextCursor  *string     `json:"next_cursor"`
}

// LastError is a chain's most recent ingest failure.
type LastError struct {
	Kind    string `json:"kind"`
	Message string `json:"message"`
	At      string `json:"at"`
}

// ChainHealth is per-chain ingest progress.
type ChainHealth struct {
	Chain           Chain            `json:"chain"`
	Head            *int64           `json:"head"`
	Anchor          int64            `json:"anchor"`
	LowerBound      int64            `json:"lower_bound"`
	ForwardPosition *int64           `json:"forward_position"`
	HistoryPosition *int64           `json:"history_position"`
	BacklogBlocks   *int64           `json:"backlog_blocks"`
	LagSeconds      *int64           `json:"lag_seconds"`
	GapsOpen        int64            `json:"gaps_open"`
	GapsByReason    map[string]int64 `json:"gaps_by_reason"`
	LastError       *LastError       `json:"last_error"`
	BudgetPaused    bool             `json:"budget_paused"`
}

// ParserHealth is per-decoder progress.
type ParserHealth struct {
	Chain                 Chain  `json:"chain"`
	Module                string `json:"module"`
	Version               string `json:"version"`
	Position              *int64 `json:"position"`
	LagBlocks             *int64 `json:"lag_blocks"`
	Quarantined           int64  `json:"quarantined"`
	Failures              int64  `json:"failures"`
	UnknownDiscriminators int64  `json:"unknown_discriminators"`
}

// ParseFailure is one failed decode task.
type ParseFailure struct {
	ID            string `json:"id"`
	Chain         Chain  `json:"chain"`
	Module        string `json:"module"`
	Position      string `json:"position"`
	Error         string `json:"error"`
	Attempts      int64  `json:"attempts"`
	Status        string `json:"status"`
	LastAttemptAt string `json:"last_attempt_at"`
}

// Budget is the monthly data-source budget state.
type Budget struct {
	Month    string `json:"month"`
	SpentUSD string `json:"spent_usd"`
	LimitUSD string `json:"limit_usd"`
	State    string `json:"state"`
}

// AdminHealth is the run quality page.
type AdminHealth struct {
	AsOf     string         `json:"as_of"`
	Chains   []ChainHealth  `json:"chains"`
	Parsers  []ParserHealth `json:"parsers"`
	Failures []ParseFailure `json:"failures"`
	Budget   Budget         `json:"budget"`
}
