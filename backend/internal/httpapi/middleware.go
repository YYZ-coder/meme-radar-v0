package httpapi

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"log/slog"
	"net"
	"net/http"
	"time"

	"github.com/meme-radar/radar/internal/obs"
)

// HandlerFunc is the handler shape for API routes: return an *obs.Error (or any error)
// and the server writes the ErrorResponse; never write error JSON by hand.
type HandlerFunc func(w http.ResponseWriter, r *http.Request) error

const traceHeader = "X-Trace-Id"

type statusWriter struct {
	http.ResponseWriter
	status int
	bytes  int
}

func (w *statusWriter) WriteHeader(code int) {
	if w.status == 0 {
		w.status = code
	}
	w.ResponseWriter.WriteHeader(code)
}

func (w *statusWriter) Write(b []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	n, err := w.ResponseWriter.Write(b)
	w.bytes += n
	return n, err
}

// withObservability is the outermost middleware: trace id → panic recovery → access log.
func withObservability(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		id := obs.ParseTraceID(r.Header.Get(traceHeader), r.Header.Get("traceparent"))
		if id == "" {
			id = obs.NewTraceID()
		}
		ctx := obs.WithTrace(r.Context(), id)
		r = r.WithContext(ctx)
		w.Header().Set(traceHeader, id)
		sw := &statusWriter{ResponseWriter: w}

		defer func() {
			if rec := recover(); rec != nil {
				err := obs.Recovered(ctx, "http "+routeOf(r), rec)
				if sw.status == 0 {
					writeError(sw, r, err)
				}
			}
			level := slog.LevelInfo
			if sw.status >= 500 {
				level = slog.LevelError
			} else if sw.status >= 400 {
				level = slog.LevelWarn
			}
			slog.Log(ctx, level, "http request",
				slog.String("method", r.Method),
				slog.String("route", routeOf(r)),
				slog.Int("status", sw.status),
				slog.Int64(obs.KeyDuration, time.Since(start).Milliseconds()),
				slog.Int("bytes", sw.bytes),
				slog.String("ip_hash", ipHash(r.RemoteAddr)),
			)
		}()
		next.ServeHTTP(sw, r)
	})
}

// routeOf returns the matched mux pattern (low cardinality), never the raw path.
func routeOf(r *http.Request) string {
	if r.Pattern != "" {
		return r.Pattern
	}
	return "unmatched"
}

func ipHash(remote string) string {
	host, _, err := net.SplitHostPort(remote)
	if err != nil {
		host = remote
	}
	sum := sha256.Sum256([]byte(host))
	return hex.EncodeToString(sum[:6])
}

type errorBody struct {
	Error struct {
		Code    obs.Code `json:"code"`
		Message string   `json:"message"`
		Field   *string  `json:"field"`
		TraceID string   `json:"trace_id"`
	} `json:"error"`
}

// writeError renders any error as the contract's ErrorResponse. Internal causes are
// logged with the trace id and never sent to the client.
func writeError(w http.ResponseWriter, r *http.Request, err error) {
	e := obs.As(err)
	if !e.Code.IsAPI() {
		e = &obs.Error{Code: obs.CodeInternal, Message: "internal error", Err: err}
	}
	spec := obs.Catalog[e.Code]
	attrs := []any{slog.String(obs.KeyCode, string(e.Code))}
	if e.Err != nil {
		attrs = append(attrs, slog.String(obs.KeyErr, e.Err.Error()))
	}
	slog.Log(r.Context(), spec.Level, e.Message, attrs...)

	var body errorBody
	body.Error.Code = e.Code
	body.Error.Message = e.Message
	if e.Field != "" {
		body.Error.Field = &e.Field
	}
	body.Error.TraceID = obs.TraceID(r.Context())
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(e.Code.Status())
	_ = json.NewEncoder(w).Encode(body)
}

// adapt turns a HandlerFunc into an http.Handler.
func adapt(h HandlerFunc) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := h(w, r); err != nil {
			writeError(w, r, err)
		}
	})
}
