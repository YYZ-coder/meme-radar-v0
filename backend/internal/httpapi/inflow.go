package httpapi

import (
	"container/list"
	"context"
	"crypto/rand"
	"encoding/hex"
	"net/http"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/meme-radar/radar/internal/api"
)

const (
	snapshotRows = 2000
	snapshotTTL  = 10 * time.Minute
	snapshotMax  = 512
)

// snapshot is one frozen, sorted inflow query result (01-api §4).
type snapshot struct {
	id          string
	rows        []api.InflowRow
	truncated   bool
	listVersion string
	asOf        string
	chainAsOf   api.ChainTimes
	window      string
	native      bool
	created     time.Time
	load        func(context.Context, []api.TokenKey) ([]api.InflowRow, error)
	mu          sync.Mutex
	loaded      map[string]api.InflowRow
	total       int
	readPaged   func(context.Context, int, int) ([]api.InflowRow, error)
}

// snapshots is a bounded LRU of query snapshots.
type snapshots struct {
	mu    sync.Mutex
	order *list.List
	byID  map[string]*list.Element
}

func newSnapshots() *snapshots {
	return &snapshots{order: list.New(), byID: map[string]*list.Element{}}
}

func (c *snapshots) put(s *snapshot) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.byID[s.id] = c.order.PushFront(s)
	for c.order.Len() > snapshotMax {
		old := c.order.Remove(c.order.Back()).(*snapshot)
		delete(c.byID, old.id)
	}
}

// get returns a live snapshot, or nil when it is unknown or older than the TTL.
func (c *snapshots) get(id string, now time.Time) *snapshot {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.byID[id]
	if !ok {
		return nil
	}
	s := e.Value.(*snapshot)
	if now.Sub(s.created) >= snapshotTTL {
		c.order.Remove(e)
		delete(c.byID, id)
		return nil
	}
	c.order.MoveToFront(e)
	return s
}

func expired(message string) *api.Error {
	return api.Errorf(http.StatusGone, "query_expired", "", message)
}

var decimalRe = regexp.MustCompile(`^-?\d+(\.\d+)?$`)

type rangeField struct {
	name    string
	integer bool
	value   func(api.InflowRow) *float64
}

func fnum(s *string) *float64 {
	if s == nil {
		return nil
	}
	v, err := strconv.ParseFloat(*s, 64)
	if err != nil {
		return nil
	}
	return &v
}

func inum(n *int64) *float64 {
	if n == nil {
		return nil
	}
	v := float64(*n)
	return &v
}

var rangeFields = []rangeField{
	{"market_cap", false, func(r api.InflowRow) *float64 { return fnum(r.MarketCap) }},
	{"price_change", false, func(r api.InflowRow) *float64 { return fnum(r.PriceChangePct) }},
	{"liquidity", false, func(r api.InflowRow) *float64 { return fnum(r.Liquidity) }},
	{"trades", true, func(r api.InflowRow) *float64 { return inum(r.Trades) }},
	{"volume", false, func(r api.InflowRow) *float64 { return fnum(r.Volume) }},
	{"net_inflow", false, func(r api.InflowRow) *float64 { return fnum(r.NetInflow) }},
	{"sm_holders", true, func(r api.InflowRow) *float64 { return inum(r.SMHolders) }},
	{"sm_trades", true, func(r api.InflowRow) *float64 { return inum(r.SMTrades) }},
	{"holders", true, func(r api.InflowRow) *float64 { return inum(r.Holders) }},
	{"age", true, func(r api.InflowRow) *float64 { return inum(r.AgeSeconds) }},
}

var sortKeys = map[string]func(api.InflowRow) *float64{
	"age":        func(r api.InflowRow) *float64 { return inum(r.AgeSeconds) },
	"sm_holders": func(r api.InflowRow) *float64 { return inum(r.SMHolders) },
	"sm_trades":  func(r api.InflowRow) *float64 { return inum(r.SMTrades) },
	"market_cap": func(r api.InflowRow) *float64 { return fnum(r.MarketCap) },
	"liquidity":  func(r api.InflowRow) *float64 { return fnum(r.Liquidity) },
	"trades":     func(r api.InflowRow) *float64 { return inum(r.Trades) },
	"volume":     func(r api.InflowRow) *float64 { return fnum(r.Volume) },
	"net_inflow": func(r api.InflowRow) *float64 { return fnum(r.NetInflow) },
}

var socialKeys = []string{"twitter", "telegram", "github", "website"}

type bound struct {
	field    rangeField
	min, max *float64
}

// inflowQuery is a parsed first-page request.
type inflowQuery struct {
	chain     api.Chain
	window    string
	native    bool
	sort      func(api.InflowRow) *float64
	sortName  string
	desc      bool
	dexes     []string
	platforms []string
	socials   []string
	devStatus string
	bounds    []bound
}

func csv(q string) []string {
	var out []string
	for _, v := range strings.Split(q, ",") {
		if v = strings.TrimSpace(v); v != "" {
			out = append(out, v)
		}
	}
	return out
}

func queryCurrency(v string) (bool, error) {
	switch v {
	case "", "usd":
		return false, nil
	case "native":
		return true, nil
	}
	return false, badParam("currency", "currency must be usd or native")
}

func parseInflowQuery(r *http.Request) (inflowQuery, error) {
	q := r.URL.Query()
	var iq inflowQuery
	var err error
	if iq.chain, err = queryChain(r); err != nil {
		return iq, err
	}
	if iq.window, err = queryWindow(r); err != nil {
		return iq, err
	}
	if iq.native, err = queryCurrency(q.Get("currency")); err != nil {
		return iq, err
	}
	sortName := q.Get("sort")
	if sortName == "" {
		sortName = "sm_trades"
	}
	if iq.sort = sortKeys[sortName]; iq.sort == nil {
		return iq, badParam("sort", "unknown sort key")
	}
	iq.sortName = sortName
	switch q.Get("order") {
	case "", "desc":
		iq.desc = true
	case "asc":
	default:
		return iq, badParam("order", "order must be asc or desc")
	}
	iq.dexes, iq.platforms, iq.socials = csv(q.Get("dex")), csv(q.Get("platform")), csv(q.Get("socials"))
	for _, s := range iq.socials {
		if !slices.Contains(socialKeys, s) {
			return iq, badParam("socials", "socials must be twitter, telegram, github or website")
		}
	}
	switch iq.devStatus = q.Get("dev_status"); iq.devStatus {
	case "", "sold", "holding":
	default:
		return iq, badParam("dev_status", "dev_status must be sold or holding")
	}
	for _, f := range rangeFields {
		b := bound{field: f}
		for _, side := range []string{"min", "max"} {
			name := side + "_" + f.name
			raw := q.Get(name)
			if raw == "" {
				continue
			}
			if !decimalRe.MatchString(raw) || (f.integer && strings.ContainsAny(raw, ".-")) {
				return iq, badParam(name, name+" must be a number")
			}
			v, _ := strconv.ParseFloat(raw, 64)
			if side == "min" {
				b.min = &v
			} else {
				b.max = &v
			}
		}
		if b.min != nil && b.max != nil && *b.min > *b.max {
			return iq, api.Errorf(http.StatusBadRequest, "invalid_range", "min_"+f.name, "min greater than max")
		}
		if b.min != nil || b.max != nil {
			iq.bounds = append(iq.bounds, b)
		}
	}
	return iq, nil
}

func (iq inflowQuery) keep(p api.ProjectionRow) bool {
	if len(iq.platforms) > 0 && !slices.Contains(iq.platforms, p.Platform) {
		return false
	}
	if len(iq.dexes) > 0 && !slices.ContainsFunc(p.DexIDs, func(id string) bool { return slices.Contains(iq.dexes, id) }) {
		return false
	}
	for _, s := range iq.socials {
		so := p.Row.Socials
		if map[string]*string{"twitter": so.Twitter, "telegram": so.Telegram, "github": so.Github, "website": so.Website}[s] == nil {
			return false
		}
	}
	if iq.devStatus != "" {
		if p.DevHoldingPct == nil || (iq.devStatus == "sold") != (*p.DevHoldingPct == 0) {
			return false
		}
	}
	for _, b := range iq.bounds {
		v := b.field.value(p.Row)
		if v == nil || (b.min != nil && *v < *b.min) || (b.max != nil && *v > *b.max) {
			return false
		}
	}
	return true
}

func rowKey(r api.InflowRow) string { return string(r.Token.Chain) + ":" + r.Token.Address }

// sorted orders rows by the sort key; nulls last regardless of direction, ties by key.
func (iq inflowQuery) sorted(rows []api.InflowRow) {
	slices.SortStableFunc(rows, func(a, b api.InflowRow) int {
		va, vb := iq.sort(a), iq.sort(b)
		switch {
		case va == nil && vb == nil:
		case va == nil:
			return 1
		case vb == nil:
			return -1
		case *va != *vb:
			if (*va < *vb) == iq.desc {
				return 1
			}
			return -1
		}
		return strings.Compare(rowKey(a), rowKey(b))
	})
}

func newQueryID() string {
	var b [8]byte
	_, _ = rand.Read(b[:])
	return "q_" + hex.EncodeToString(b[:])
}

func (s *server) inflow(w http.ResponseWriter, r *http.Request) {
	limit, err := queryInt(r, "limit", 50, 1, 100)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	now := s.opt.Now()
	if id := r.URL.Query().Get("query_id"); id != "" {
		offset, err := queryInt(r, "offset", 0, 0, snapshotRows)
		if err != nil {
			s.fail(w, r, err)
			return
		}
		snap := s.snaps.get(id, now)
		if snap == nil {
			s.fail(w, r, expired("query snapshot expired or unknown; re-query"))
			return
		}
		page, err := snap.readPage(r.Context(), offset, limit)
		if err != nil {
			s.fail(w, r, err)
			return
		}
		s.json(w, http.StatusOK, page)
		return
	}
	iq, err := parseInflowQuery(r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	// The reported default ordering needs no application-side filters. Read
	// only its page in PostgreSQL; other queries retain the general selector.
	if iq.sortName == "sm_trades" && iq.desc && len(iq.bounds) == 0 && len(iq.dexes) == 0 && len(iq.platforms) == 0 && len(iq.socials) == 0 && iq.devStatus == "" {
		if source, ok := s.b.(interface {
			InflowTradePages(context.Context, api.Chain, string, bool, int) (api.InflowPages, error)
		}); ok {
			set, err := source.InflowTradePages(r.Context(), iq.chain, iq.window, iq.native, limit)
			if err != nil {
				s.fail(w, r, err)
				return
			}
			snap := &snapshot{id: newQueryID(), truncated: set.Truncated, listVersion: set.ListVersion, asOf: set.AsOf, chainAsOf: set.ChainAsOf, window: iq.window, native: iq.native, created: now, total: set.Total, readPaged: set.LoadPage}
			page, err := snap.readPage(r.Context(), 0, limit)
			if err != nil {
				s.fail(w, r, err)
				return
			}
			s.snaps.put(snap)
			s.json(w, http.StatusOK, page)
			return
		}
	}
	var proj api.Projection
	if selector, ok := s.b.(interface {
		InflowSelection(context.Context, api.Chain, string, bool) (api.Projection, error)
	}); ok {
		proj, err = selector.InflowSelection(r.Context(), iq.chain, iq.window, iq.native)
	} else {
		proj, err = s.b.InflowProjection(r.Context(), iq.chain, iq.window, iq.native)
	}
	if err != nil {
		s.fail(w, r, err)
		return
	}
	rows := make([]api.InflowRow, 0, len(proj.Rows))
	for _, p := range proj.Rows {
		if iq.keep(p) {
			rows = append(rows, p.Row)
		}
	}
	iq.sorted(rows)
	snap := &snapshot{
		id: newQueryID(), truncated: len(rows) > snapshotRows, listVersion: proj.ListVersion,
		asOf: proj.AsOf, chainAsOf: proj.ChainAsOf, window: iq.window, native: iq.native, created: now,
	}
	snap.rows = rows[:min(len(rows), snapshotRows)]
	snap.load = proj.LoadRows
	snap.loaded = map[string]api.InflowRow{}
	page, err := snap.readPage(r.Context(), 0, limit)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.snaps.put(snap)
	s.json(w, http.StatusOK, page)
}

func (sn *snapshot) page(offset, limit int) api.InflowPage {
	total := len(sn.rows)
	if sn.readPaged != nil {
		total = sn.total
	}
	end := min(offset+limit, total)
	p := api.InflowPage{
		QueryID: sn.id, ListVersion: sn.listVersion, AsOf: sn.asOf, ChainAsOf: sn.chainAsOf,
		Window: sn.window, Total: int64(total), Truncated: sn.truncated, Offset: int64(offset),
		Items: []api.InflowRow{},
	}
	if offset < end && sn.readPaged == nil {
		p.Items = sn.rows[offset:end]
	}
	if end < total {
		next := int64(end)
		p.NextOffset = &next
	}
	return p
}

// readPage resolves payloads outside the lock. Only immutable values enter the
// page cache; the ordered identities and query metadata never change.
func (sn *snapshot) readPage(ctx context.Context, offset, limit int) (api.InflowPage, error) {
	page := sn.page(offset, limit)
	if sn.readPaged != nil {
		wanted := min(offset+limit, sn.total) - offset
		if wanted <= 0 {
			return page, nil
		}
		rows, err := sn.readPaged(ctx, offset, wanted)
		if err != nil {
			return api.InflowPage{}, err
		}
		if len(rows) != wanted {
			return api.InflowPage{}, api.Errorf(http.StatusServiceUnavailable, "busy", "", "published query rows unavailable")
		}
		page.Items = rows
		return page, nil
	}
	if sn.load == nil || len(page.Items) == 0 {
		return page, nil
	}
	keys := make([]api.TokenKey, 0, len(page.Items))
	sn.mu.Lock()
	for _, row := range page.Items {
		if _, ok := sn.loaded[rowKey(row)]; !ok {
			keys = append(keys, api.TokenKey{Chain: row.Token.Chain, Address: row.Token.Address})
		}
	}
	sn.mu.Unlock()
	if len(keys) > 0 {
		rows, err := sn.load(ctx, keys)
		if err != nil {
			return api.InflowPage{}, err
		}
		sn.mu.Lock()
		for _, row := range rows {
			sn.loaded[rowKey(row)] = row
		}
		sn.mu.Unlock()
	}
	items := make([]api.InflowRow, 0, len(page.Items))
	sn.mu.Lock()
	defer sn.mu.Unlock()
	for _, key := range page.Items {
		row, ok := sn.loaded[rowKey(key)]
		if !ok {
			return api.InflowPage{}, api.Errorf(http.StatusServiceUnavailable, "busy", "", "published query rows unavailable")
		}
		items = append(items, row)
	}
	page.Items = items
	return page, nil
}
