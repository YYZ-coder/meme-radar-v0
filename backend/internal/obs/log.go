package obs

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
)

// LogOptions configures Setup. File is optional; when set, logs go to stdout and the file.
type LogOptions struct {
	Level   string
	File    string
	Service string
	Role    string
	Version string
}

// Field names shared by every package (18-observability §2). Use these instead of literals.
const (
	KeyTraceID   = "trace_id"
	KeyParent    = "parent_trace_id"
	KeyCode      = "code"
	KeyChain     = "chain"
	KeyModule    = "module"
	KeyJobID     = "job_id"
	KeyBlock     = "block"
	KeyPosition  = "position"
	KeyToken     = "token"
	KeyWallet    = "wallet"
	KeyQueryID   = "query_id"
	KeyListVer   = "list_version"
	KeyErr       = "err"
	KeyStack     = "stack"
	KeyTask      = "task"
	KeyDuration  = "duration_ms"
	redactedText = "[redacted]"
)

// Keys whose values must never reach a log sink (case-insensitive substring match).
var sensitive = []string{"authorization", "password", "secret", "private", "mnemonic", "seed", "api_key", "apikey", "cookie", "admin_token", "bearer"}

func isSensitive(key string) bool {
	k := strings.ToLower(key)
	if k == "token" || strings.HasSuffix(k, "_token") {
		return k != KeyToken // "token" is a token address in this codebase
	}
	for _, s := range sensitive {
		if strings.Contains(k, s) {
			return true
		}
	}
	return false
}

func redact(_ []string, a slog.Attr) slog.Attr {
	if isSensitive(a.Key) {
		return slog.String(a.Key, redactedText)
	}
	return a
}

// contextHandler adds trace ids and ctx attrs to every record.
type contextHandler struct{ slog.Handler }

func (h contextHandler) Handle(ctx context.Context, r slog.Record) error {
	if id := TraceID(ctx); id != "" {
		r.AddAttrs(slog.String(KeyTraceID, id))
	}
	if id := ParentTraceID(ctx); id != "" {
		r.AddAttrs(slog.String(KeyParent, id))
	}
	r.AddAttrs(ctxAttrs(ctx)...)
	return h.Handler.Handle(ctx, r)
}

func (h contextHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return contextHandler{h.Handler.WithAttrs(attrs)}
}

func (h contextHandler) WithGroup(name string) slog.Handler {
	return contextHandler{h.Handler.WithGroup(name)}
}

// NewLogger builds the JSON logger on w (exported for tests and tools).
func NewLogger(w io.Writer, opts LogOptions) *slog.Logger {
	var level slog.Level
	if err := level.UnmarshalText([]byte(opts.Level)); err != nil {
		level = slog.LevelInfo
	}
	base := slog.NewJSONHandler(w, &slog.HandlerOptions{Level: level, ReplaceAttr: redact})
	return slog.New(contextHandler{base}).With(
		slog.String("service", orDefault(opts.Service, "radar")),
		slog.String("role", orDefault(opts.Role, "all")),
		slog.String("version", orDefault(opts.Version, "dev")),
	)
}

// Setup installs the process-wide logger and returns a closer for the log file.
func Setup(opts LogOptions) (*slog.Logger, func() error, error) {
	var w io.Writer = os.Stdout
	closer := func() error { return nil }
	if opts.File != "" {
		if err := os.MkdirAll(filepath.Dir(opts.File), 0o755); err != nil {
			return nil, closer, fmt.Errorf("log dir: %w", err)
		}
		f, err := os.OpenFile(opts.File, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
		if err != nil {
			return nil, closer, fmt.Errorf("open log file: %w", err)
		}
		w = io.MultiWriter(os.Stdout, f)
		closer = f.Close
	}
	logger := NewLogger(w, opts)
	slog.SetDefault(logger)
	return logger, closer, nil
}

func orDefault(v, d string) string {
	if v == "" {
		return d
	}
	return v
}
