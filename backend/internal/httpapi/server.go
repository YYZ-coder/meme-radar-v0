// Package httpapi serves api/openapi.yaml over net/http.
package httpapi

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/meme-radar/radar/internal/api"
	"github.com/meme-radar/radar/internal/metrics"
)

// Options configures the server.
type Options struct {
	// AdminToken guards /api/v1/admin/*; empty denies every admin request.
	AdminToken string
	// AdminDisabled rejects admin routes with 401, even if a token is supplied.
	AdminDisabled bool
	// Now returns the current time; defaults to time.Now.
	Now func() time.Time
	// Logger receives request errors; defaults to slog.Default().
	Logger *slog.Logger
	// DemoData marks every response with X-Radar-Mock: 1 (D110).
	DemoData bool
	// QueryTimeout bounds Backend work per request; defaults to two seconds.
	QueryTimeout time.Duration
}

// Backend supplies the data behind every endpoint. Implementations must be safe
// for concurrent use; errors of type *api.Error are returned to the client as is.
type Backend interface {
	NativePrices(ctx context.Context) (api.NativePrices, error)
	Search(ctx context.Context, q string, chain api.Chain, limit int) (api.Search, error)
	Overview(ctx context.Context, chain api.Chain, window string) (api.Overview, error)
	Facets(ctx context.Context, chain api.Chain, window string) (api.Facets, error)
	InflowProjection(ctx context.Context, chain api.Chain, window string, native bool) (api.Projection, error)
	InflowRows(ctx context.Context, window string, native bool, keys []api.TokenKey) ([]api.InflowRow, error)
	ActiveListVersion(ctx context.Context) (string, error)
	TokenDetail(ctx context.Context, chain api.Chain, address string, native bool) (api.TokenDetail, error)
	Candles(ctx context.Context, chain api.Chain, address string, q api.CandleQuery) (api.Candles, error)
	Activities(ctx context.Context, chain api.Chain, address string, q api.ActivityQuery) (api.Activities, error)
	Holders(ctx context.Context, chain api.Chain, address, label, cursor string, limit int) (api.Holders, error)
	Pools(ctx context.Context, chain api.Chain, address string) (api.Pools, error)
	LiquidityEvents(ctx context.Context, chain api.Chain, address, cursor string, limit int) (api.LiquidityEvents, error)
	TokenSmartMoney(ctx context.Context, chain api.Chain, address string) (api.TokenSmartMoney, error)
	Health(ctx context.Context) (api.AdminHealth, error)
	RetryFailure(ctx context.Context, id string) (api.ParseFailure, error)
	CreateImport(ctx context.Context, fileName string, content []byte) (api.ImportReport, error)
	Import(ctx context.Context, id string) (api.ImportReport, error)
	ApplyImport(ctx context.Context, id string) (api.ListVersionChange, error)
	ListBuild(ctx context.Context, version string) (api.ListBuildStatus, error)
	RetryListBuild(ctx context.Context, version string) (api.ListBuildStatus, error)
	Members(ctx context.Context, chain api.Chain, source, tag, q, cursor string, limit int) (api.Members, error)
	Exclusions(ctx context.Context, chain api.Chain) (api.Exclusions, error)
	Exclude(ctx context.Context, chain api.Chain, address, reason string) (api.ListVersionChange, error)
	Restore(ctx context.Context, chain api.Chain, address string) (api.ListVersionChange, error)
	RemoveMember(ctx context.Context, chain api.Chain, address string) (api.ListVersionChange, error)
	ListChange(ctx context.Context, version string) (api.ListVersionChange, error)
}

// HealthChecker optionally supplies database readiness for /healthz.
// Backends without external dependencies retain the process-only check.
type HealthChecker interface {
	CheckHealth(ctx context.Context) error
}

type server struct {
	b     Backend
	opt   Options
	log   *slog.Logger
	snaps *snapshots
	idem  *idempotency
}

// New returns the HTTP handler for the API.
func New(b Backend, o Options) http.Handler {
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.Logger == nil {
		o.Logger = slog.Default()
	}
	o.Logger = o.Logger.With("role", "api")
	if o.QueryTimeout <= 0 {
		o.QueryTimeout = 2 * time.Second
	}
	s := &server{b: b, opt: o, log: o.Logger, snaps: newSnapshots(), idem: &idempotency{byKey: map[string]idemEntry{}}}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", s.healthz)
	mux.HandleFunc("GET /api/v1/market/natives", s.nativePrices)
	mux.HandleFunc("GET /api/v1/search", s.search)
	mux.HandleFunc("GET /api/v1/smart-money/overview", s.overview)
	mux.HandleFunc("GET /api/v1/smart-money/inflow/facets", s.facets)
	mux.HandleFunc("GET /api/v1/smart-money/inflow", s.inflow)
	mux.HandleFunc("POST /api/v1/tokens/batch", s.batch)
	s.tokenRoutes(mux)
	s.adminRoutes(mux)
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		s.fail(w, r, api.Errorf(http.StatusNotFound, "not_found", "", "no such endpoint"))
	})
	return s.middleware(mux)
}

type traceKey struct{}

var traceRe = regexp.MustCompile(`^[0-9a-f]{32}$`)

// traceID reuses a valid incoming X-Trace-Id or traceparent trace id, else makes one.
func traceID(r *http.Request) string {
	for _, v := range []string{
		strings.ToLower(r.Header.Get("X-Trace-Id")),
		strings.ToLower(strings.Split(r.Header.Get("traceparent")+"--", "-")[1]),
	} {
		if traceRe.MatchString(v) && strings.Trim(v, "0") != "" {
			return v
		}
	}
	var b [16]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

func (s *server) middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		started := time.Now()
		observed := &responseStats{ResponseWriter: w}
		w = observed
		defer func() {
			route := r.Pattern
			if route == "" {
				route = "unmatched"
			}
			status := observed.status
			if status == 0 {
				status = http.StatusOK
			}
			metrics.APIRequests.WithLabelValues(route, strconv.Itoa(status)).Inc()
			metrics.APISeconds.WithLabelValues(route).Observe(time.Since(started).Seconds())
		}()

		id := traceID(r)
		w.Header().Set("X-Trace-Id", id)
		w.Header().Set("Cache-Control", "no-store")
		if s.opt.DemoData {
			w.Header().Set("X-Radar-Mock", "1")
		}
		ctx, cancel := context.WithTimeout(r.Context(), s.opt.QueryTimeout)
		defer cancel()
		ctx = context.WithValue(ctx, dataTimeKey{}, &dataTime{})
		r = r.WithContext(context.WithValue(ctx, traceKey{}, id))
		defer func() {
			if v := recover(); v != nil {
				s.log.Error("panic serving request", "trace_id", id, "path", r.URL.Path, "chain", r.PathValue("chain"), "panic", v)
				s.fail(w, r, api.Errorf(http.StatusInternalServerError, "internal", "", "internal error"))
			}
		}()
		next.ServeHTTP(w, r)
	})
}

func (s *server) json(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		s.log.Error("encode response", "err", err)
	}
}

// fail writes err as ErrorResponse; non-API errors become 500 without details.
func (s *server) fail(w http.ResponseWriter, r *http.Request, err error) {
	id, _ := r.Context().Value(traceKey{}).(string)
	var e *api.Error
	if !errors.As(err, &e) {
		if errors.Is(err, context.DeadlineExceeded) {
			e = api.Errorf(http.StatusServiceUnavailable, "busy", "", "query timed out")
		} else {
			s.log.Error("request failed", "trace_id", id, "path", r.URL.Path, "chain", r.PathValue("chain"), "err", err)
			e = api.Errorf(http.StatusInternalServerError, "internal", "", "internal error")
		}
	}
	body := api.ErrorBody{Error: api.ErrorDetail{Code: e.Code, Message: e.Message, TraceID: id}}
	if e.Field != "" {
		body.Error.Field = &e.Field
	}
	if e.Status == http.StatusServiceUnavailable || e.Status == http.StatusTooManyRequests {
		w.Header().Set("Retry-After", "2")
	}
	s.json(w, e.Status, body)
}

func (s *server) healthz(w http.ResponseWriter, r *http.Request) {
	body := api.Health{Status: "ok"}
	if checker, ok := s.b.(HealthChecker); ok {
		body.DB = "ok"
		if err := checker.CheckHealth(r.Context()); err != nil {
			s.json(w, http.StatusServiceUnavailable, api.Health{Status: "degraded", DB: "unavailable"})
			return
		}
	}
	s.json(w, http.StatusOK, body)
}
