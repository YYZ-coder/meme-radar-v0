package mockdata

import (
	"math"
	"strings"
	"time"

	"github.com/meme-radar/radar/internal/api"
)

type platform struct {
	id, name string
	chain    api.Chain
	curve    bool
}

type dex struct {
	id, name, protocol, quote string
	chain                     api.Chain
}

var platforms = []platform{
	{"pump_fun", "Pump.fun", api.Solana, true},
	{"raydium_launchlab", "Raydium LaunchLab", api.Solana, true},
	{"clanker", "Clanker", api.Base, false},
	{"four_meme", "Four.meme", api.BSC, true},
}

var dexes = []dex{
	{"pumpswap", "PumpSwap", "pumpswap", "SOL", api.Solana},
	{"pump_curve", "Pump", "pump_curve", "SOL", api.Solana},
	{"raydium_cpmm", "Raydium CPMM", "raydium_cpmm", "SOL", api.Solana},
	{"launchlab_curve", "LaunchLab", "raydium_launchlab", "SOL", api.Solana},
	{"uniswap_v4", "Uniswap v4", "uniswap_v4", "ETH", api.Base},
	{"pancakeswap_v2", "PancakeSwap v2", "pancakeswap_v2", "BNB", api.BSC},
	{"four_meme_curve", "Four.meme", "four_meme", "BNB", api.BSC},
}

var nativeUSD = map[api.Chain]float64{api.Solana: 105.247, api.Base: 2454.07, api.BSC: 729.4}
var nativeSymbol = map[api.Chain]string{api.Solana: "SOL", api.Base: "ETH", api.BSC: "BNB"}

func platformByID(id string) *platform {
	for i := range platforms {
		if platforms[i].id == id {
			return &platforms[i]
		}
	}
	return nil
}

func dexByID(id string) *dex {
	for i := range dexes {
		if dexes[i].id == id {
			return &dexes[i]
		}
	}
	return nil
}

type socials struct{ twitter, telegram, website, github string }

func (s socials) api() api.Socials {
	opt := func(v string) *string {
		if v == "" {
			return nil
		}
		return &v
	}
	return api.Socials{Twitter: opt(s.twitter), Telegram: opt(s.telegram), Website: opt(s.website), Github: opt(s.github)}
}

func (s socials) has(name string) bool {
	switch name {
	case "twitter":
		return s.twitter != ""
	case "telegram":
		return s.telegram != ""
	case "website":
		return s.website != ""
	case "github":
		return s.github != ""
	}
	return false
}

// token is one mock token; ms values are Unix milliseconds like the TS mock.
type token struct {
	chain         api.Chain
	address       string
	symbol, name  string
	platform      string
	dexes         []string
	graduated     bool
	launchAt      float64
	supply        float64
	decimals      int64
	basePrice     *float64
	baseLiquidity *float64
	tradesPerHour float64
	smShare       float64
	smHolders     int64
	holders       int64
	socials       socials
	creator       string
	devHoldingPct *float64
	seed          uint32
	sample        bool
}

const day = 86400_000.0

// epoch fixes generated launch times; ages move with the clock.
var epoch = float64(time.Date(2026, 10, 19, 0, 0, 0, 0, time.UTC).UnixMilli())

var words = strings.Fields("PEPE WIF BONK POPCAT MOODENG BRETT TOSHI ANDY FLOKI BANANA GIGA MEW PONKE SLERF MOTHER DEGEN HIGHER KEYCAT MIGGLES SKI CHILLGUY GOAT PNUT FWOG MICHI RETARDIO LOCKIN SPX HOSICO BOME NEIRO TURBO DOGINME BENJI TYBG BASED ROCKY CATE BABYDOGE TST MUBARAK BROCCOLI CHEEMS SIREN TUT BANK FORM PIXEL RADAR ALPHA")
var suffixes = []string{"", "", "", " INU", " AI", " CAT", " 2.0", " DAO", " ON SOL", " CLUB"}

func seedTokens() []*token {
	now := epoch
	f := func(v float64) *float64 { return &v }
	return []*token{
		{chain: api.Solana, address: "GX1JGwFmrzgkv6MBjwZZdZt3qBcWi1d5ZuAUS3KFpump", symbol: "GX1J", name: "GX1J (sample)", platform: "pump_fun", dexes: []string{"pump_curve"}, launchAt: now - 3*3600_000, supply: 955336024.2689, decimals: 6, basePrice: f(0.0000322262), baseLiquidity: f(14582.94), tradesPerHour: 380, smShare: 0.06, smHolders: 7, holders: 412, socials: socials{twitter: "https://x.com/"}, creator: "4v9uPGeHMooSQiL2rV1LeN4qN5Li5c1TNCh6JmkvBuu6", devHoldingPct: f(2.4), seed: hash("GX1J"), sample: true},
		{chain: api.Solana, address: "HWgMx9sQqk5zHBtjJcfGRskKkwfx927Q2XHvtxg9pump", symbol: "HWGMX", name: "HWgMx (sample)", platform: "pump_fun", dexes: []string{"pump_curve"}, launchAt: now - 40*60_000, supply: 1_000_000_000, decimals: 6, tradesPerHour: 260, smShare: 0.08, smHolders: 3, holders: 96, creator: solanaAddress("creator-hwgmx", ""), seed: hash("HWGMX"), sample: true},
		{chain: api.Solana, address: "EvJBc8govNsZ4fi5SSKp3BPXg9AVyssipm16c2GPbg4e", symbol: "EVJB", name: "EvJB (sample)", platform: "pump_fun", dexes: []string{"pumpswap"}, graduated: true, launchAt: now - 9*day, supply: 1_000_000_000, decimals: 6, basePrice: f(0.0000171136), baseLiquidity: f(24832.65), tradesPerHour: 140, smShare: 0.05, smHolders: 4, holders: 1288, socials: socials{twitter: "https://x.com/", telegram: "https://t.me/"}, creator: solanaAddress("creator-evjb", ""), devHoldingPct: f(0), seed: hash("EVJB"), sample: true},
		{chain: api.Base, address: "0x5b7c19533b7f62c980cecd475ba50018c32f38d3", symbol: "NODF", name: "NODF", platform: "clanker", dexes: []string{"uniswap_v4"}, graduated: true, launchAt: now - 21*day, supply: 10_000_000_000, decimals: 18, basePrice: f(0.0085483593), baseLiquidity: f(2_140_000), tradesPerHour: 210, smShare: 0.04, smHolders: 18, holders: 9120, socials: socials{twitter: "https://x.com/", website: "https://example.com"}, creator: evmAddress("creator-nodf", ""), devHoldingPct: f(1.2), seed: hash("NODF"), sample: true},
		{chain: api.Base, address: "0x7d4bfd446d4b81d5ede62529f9c8db6fdd81e222", symbol: "RIBBIT", name: "Blue Frog", platform: "clanker", dexes: []string{"uniswap_v4"}, graduated: true, launchAt: now - 63*day, supply: 1_000_000_000, decimals: 18, basePrice: f(0.0049), baseLiquidity: f(860_000), tradesPerHour: 120, smShare: 0.05, smHolders: 11, holders: 4410, socials: socials{twitter: "https://x.com/"}, creator: evmAddress("creator-ribbit", ""), devHoldingPct: f(0), seed: hash("RIBBIT"), sample: true},
		{chain: api.BSC, address: "0xf657490f3c5563895ee06833cc54d43f13ce4444", symbol: "飞行汽车", name: "飞行汽车", platform: "four_meme", dexes: []string{"four_meme_curve"}, launchAt: now - 5*3600_000, supply: 1_000_000_000, decimals: 18, basePrice: f(0.000033592993324706), baseLiquidity: f(18_420), tradesPerHour: 320, smShare: 0.07, smHolders: 5, holders: 640, socials: socials{telegram: "https://t.me/"}, creator: evmAddress("creator-flycar", ""), devHoldingPct: f(3.1), seed: hash("FLYCAR"), sample: true},
		{chain: api.BSC, address: "0x0e67e6dc4c512ebc8c11fd47f6a5445643ebffff", symbol: "sanctuary", name: "sanctuary", platform: "four_meme", dexes: []string{"four_meme_curve"}, launchAt: now - 2*3600_000, supply: 1_000_000_000, decimals: 18, basePrice: f(0.0000032097), baseLiquidity: f(6_200), tradesPerHour: 160, smShare: 0.03, smHolders: 1, holders: 77, creator: "0x47a38019f14983f5d1ef6b0fde2f31421b547647", devHoldingPct: f(12.5), seed: hash("SANCTUARY"), sample: true},
	}
}

// generated ports frontend/src/mock/data.ts generated(); r() call order is preserved.
func generated(index int) *token {
	r := rng(hash("token-" + num(float64(index))))
	chain := api.BSC
	switch {
	case index%10 < 6:
		chain = api.Solana
	case index%10 < 8:
		chain = api.Base
	}
	word := words[index%len(words)]
	symbol := word
	if index >= len(words) {
		symbol += num(float64(index % 7))
	}
	if len(symbol) > 12 {
		symbol = symbol[:12]
	}
	name := word[:1] + strings.ToLower(word[1:]) + suffixes[int(r()*float64(len(suffixes)))]
	var list []platform
	for _, p := range platforms {
		if p.chain == chain {
			list = append(list, p)
		}
	}
	var plat *platform
	if r() < 0.85 {
		plat = &list[int(r()*float64(len(list)))]
	}
	var ageDays float64
	if r() < 0.35 {
		ageDays = r() * 1.5
	} else if r() < 0.7 {
		ageDays = 1 + r()*29
	} else {
		ageDays = 30 + r()*400
	}
	graduated := true
	if plat != nil {
		graduated = !plat.curve || r() < 0.55 || ageDays > 3
	}
	var dexIDs []string
	switch chain {
	case api.Solana:
		switch {
		case plat != nil && plat.id == "raydium_launchlab" && graduated:
			dexIDs = []string{"raydium_cpmm"}
		case plat != nil && plat.id == "raydium_launchlab":
			dexIDs = []string{"launchlab_curve"}
		case graduated:
			if r() < 0.25 {
				dexIDs = []string{"pumpswap", "raydium_cpmm"}
			} else {
				dexIDs = []string{"pumpswap"}
			}
		default:
			dexIDs = []string{"pump_curve"}
		}
	case api.Base:
		dexIDs = []string{"uniswap_v4"}
	default:
		if graduated {
			dexIDs = []string{"pancakeswap_v2"}
		} else {
			dexIDs = []string{"four_meme_curve"}
		}
	}
	mcap := math.Exp(math.Log(8_000) + r()*math.Log(80_000_000/8_000.0))
	supply := 1_000_000_000.0
	if chain == api.Base && r() < 0.5 {
		supply = 100_000_000_000
	}
	unknownPrice := r() < 0.04
	unknownLiquidity := unknownPrice || r() < 0.04
	holders := int64(jsRound(40 + math.Sqrt(mcap)*(0.5+r()*2)))
	var s socials
	if r() < 0.75 {
		s.twitter = "https://x.com/"
	}
	if r() < 0.55 {
		s.telegram = "https://t.me/"
	}
	if r() < 0.4 {
		s.website = "https://example.com"
	}
	if r() < 0.08 {
		s.github = "https://github.com/"
	}
	platID := ""
	if plat != nil {
		platID = plat.id
	}
	var address, creator string
	if chain == api.Solana {
		suffix := ""
		if platID == "pump_fun" {
			suffix = "pump"
		}
		address = solanaAddress("t"+num(float64(index)), suffix)
		creator = solanaAddress("creator"+num(float64(index)), "")
	} else {
		suffix := ""
		if platID == "four_meme" {
			suffix = "4444"
		}
		address = evmAddress("t"+num(float64(index)), suffix)
		creator = evmAddress("creator"+num(float64(index)), "")
	}
	t := &token{
		chain: chain, address: address, symbol: symbol, name: name, platform: platID, dexes: dexIDs,
		graduated: graduated, launchAt: epoch - ageDays*day, supply: supply, holders: holders, socials: s,
		creator: creator, seed: hash("seed-" + num(float64(index))),
	}
	t.decimals = 18
	if chain == api.Solana {
		t.decimals = 6
	}
	if !unknownPrice {
		t.basePrice = ptr(mcap / supply)
	}
	if !unknownLiquidity {
		t.baseLiquidity = ptr(mcap * (0.05 + r()*0.12))
	}
	t.tradesPerHour = jsRound(20 + math.Sqrt(mcap)*0.15*(0.3+r()*2))
	t.smShare = 0.01 + r()*0.08
	t.smHolders = int64(jsRound(r() * math.Min(60, math.Sqrt(float64(holders)))))
	if r() < 0.2 {
		t.devHoldingPct = nil
	} else if r() < 0.4 {
		t.devHoldingPct = ptr(0.0)
	} else {
		t.devHoldingPct = ptr(math.Round(r()*9*100) / 100)
	}
	return t
}

var universe = func() []*token {
	list := seedTokens()
	for i := 0; i < 253; i++ {
		list = append(list, generated(i))
	}
	return list
}()

var byKey = func() map[string]*token {
	m := make(map[string]*token, len(universe))
	for _, t := range universe {
		m[string(t.chain)+":"+strings.ToLower(t.address)] = t
	}
	return m
}()

func findToken(chain api.Chain, address string) *token {
	return byKey[string(chain)+":"+strings.ToLower(address)]
}

var windowSeconds = map[string]float64{"1m": 60, "5m": 300, "1h": 3600, "4h": 14400, "24h": 86400}

// priceAt is a smooth deterministic price path; nil before launch or when unknown.
func priceAt(t *token, ms float64) *float64 {
	if t.basePrice == nil || ms < t.launchAt {
		return nil
	}
	sec := ms / 1000
	s := float64(t.seed % 1000)
	wave := 0.18*math.Sin(sec/5400+s) + 0.07*math.Sin(sec/900+s*2) + 0.025*math.Sin(sec/97+s*3) + 0.01*math.Sin(sec/13+s)
	since := math.Min(1, (ms-t.launchAt)/(6*3600_000))
	p := *t.basePrice * math.Exp(wave) * (0.35 + 0.65*since)
	return &p
}

func tradesIn(t *token, seconds, now float64) float64 {
	if now-t.launchAt < seconds*1000 {
		seconds = math.Max(0, (now-t.launchAt)/1000)
	}
	bucket := math.Floor(now / 10_000)
	jitter := 0.85 + 0.3*unit(t.address+":"+num(seconds)+":"+num(bucket))
	return jsRound(t.tradesPerHour * seconds * jitter / 3600)
}

type metricsT struct {
	price, priceStart, marketCap, liquidity *float64
	trades, buys, sells                     int64
	volume, netInflow                       *float64
	smTrades                                int64
	smBuyVolume, smSellVolume               *float64
	traders                                 int64
	sparkline                               []*float64
}

func metrics(t *token, window string, now float64) metricsT {
	seconds := windowSeconds[window]
	price := priceAt(t, now)
	start := now - seconds*1000
	priceStart := priceAt(t, math.Max(start, t.launchAt))
	trades := tradesIn(t, seconds, now)
	buyRatio := 0.45 + 0.25*unit(t.address+":buy:"+window+":"+num(math.Floor(now/60_000)))
	buys := jsRound(trades * buyRatio)
	m := metricsT{price: price, priceStart: priceStart, trades: int64(trades), buys: int64(buys), sells: int64(trades - buys)}
	var volume *float64
	if price != nil {
		base := 0.0
		if t.basePrice != nil {
			base = *t.basePrice
		}
		v := trades * (40 + t.supply*base/60_000)
		volume = &v
	}
	m.volume = volume
	m.smTrades = int64(jsRound(trades * t.smShare))
	smBias := unit(t.address+":sm:"+window+":"+num(math.Floor(now/30_000))) - 0.38
	if volume != nil {
		smVolume := *volume * t.smShare * 1.6
		buy := smVolume * (0.5 + smBias/2)
		sell := smVolume - buy
		m.smBuyVolume, m.smSellVolume = &buy, &sell
		net := buy - sell
		m.netInflow = &net
	}
	for i := 1; i <= 20; i++ {
		m.sparkline = append(m.sparkline, priceAt(t, start+float64(i)*seconds*1000/20))
	}
	if price != nil {
		mc := *price * t.supply
		m.marketCap = &mc
		if t.baseLiquidity != nil && t.basePrice != nil {
			l := *t.baseLiquidity * math.Sqrt(*price / *t.basePrice)
			m.liquidity = &l
		}
	}
	m.traders = int64(math.Max(map[bool]float64{true: 1, false: 0}[trades > 0], jsRound(trades*0.42)))
	return m
}

func conv(v *float64, chain api.Chain, native bool) *float64 {
	if v == nil || !native {
		return v
	}
	x := *v / nativeUSD[chain]
	return &x
}

func iso(ms float64) string {
	return time.UnixMilli(int64(ms)).UTC().Format("2006-01-02T15:04:05.000Z")
}

func tokenRef(t *token) api.TokenRef {
	ref := api.TokenRef{Chain: t.chain, Address: t.address, Symbol: t.symbol, Name: t.name, LaunchAt: ptr(iso(t.launchAt))}
	if p := platformByID(t.platform); p != nil {
		ref.LaunchPlatform = ptr(p.name)
	}
	return ref
}

func currencyOf(chain api.Chain, native bool) string {
	if native {
		return nativeSymbol[chain]
	}
	return "USD"
}

func pctChange(price, start *float64) *string {
	if price == nil || start == nil || *start == 0 {
		return nil
	}
	return ptr(dec((*price-*start) / *start * 100, 6))
}

func inflowRow(t *token, window string, native bool, now float64) api.InflowRow {
	m := metrics(t, window, now)
	c := func(v *float64) *string { return decp(conv(v, t.chain, native), 8) }
	row := api.InflowRow{
		Token: tokenRef(t), Currency: currencyOf(t.chain, native),
		Price: c(m.price), MarketCap: c(m.marketCap), PriceChangePct: pctChange(m.price, m.priceStart),
		Liquidity: c(m.liquidity), Trades: ptr(m.trades), Buys: ptr(m.buys), Sells: ptr(m.sells),
		Observed: api.Observed{Trades: m.trades, Buys: m.buys, Sells: m.sells}, Coverage: "complete",
		Volume: c(m.volume), NetInflow: c(m.netInflow), SMHolders: ptr(t.smHolders), SMTrades: ptr(m.smTrades),
		Holders: ptr(t.holders), AgeSeconds: ptr(int64(math.Max(0, math.Floor((now-t.launchAt)/1000)))),
		Socials: t.socials.api(),
	}
	for _, id := range t.dexes {
		if d := dexByID(id); d != nil {
			row.Dexes = append(row.Dexes, d.name)
		} else {
			row.Dexes = append(row.Dexes, id)
		}
	}
	for _, p := range m.sparkline {
		row.Sparkline = append(row.Sparkline, c(p))
	}
	return row
}
