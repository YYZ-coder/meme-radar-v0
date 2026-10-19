// Package obs is the shared observability layer (docs/engineering/18-observability.md):
// trace ids, structured logging, the error-code catalog and panic-safe goroutines.
// Every package logs, fails and spawns goroutines through it.
package obs

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"log/slog"
	"strings"
)

type ctxKey int

const (
	traceKey ctxKey = iota
	parentKey
	attrsKey
)

// NewTraceID returns a W3C-compatible trace id: 32 lowercase hex chars, never all zeros.
func NewTraceID() string {
	var b [16]byte
	for {
		if _, err := rand.Read(b[:]); err != nil {
			panic("obs: crypto/rand unavailable: " + err.Error())
		}
		if b != [16]byte{} {
			return hex.EncodeToString(b[:])
		}
	}
}

// ValidTraceID reports whether s is 32 lowercase hex chars and not all zeros.
func ValidTraceID(s string) bool {
	if len(s) != 32 || s == strings.Repeat("0", 32) {
		return false
	}
	for _, c := range s {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}

// ParseTraceID accepts an X-Trace-Id value or a W3C traceparent
// ("00-<trace-id>-<span-id>-<flags>"); it returns "" when neither is valid.
func ParseTraceID(xTraceID, traceparent string) string {
	if id := strings.ToLower(strings.TrimSpace(xTraceID)); ValidTraceID(id) {
		return id
	}
	parts := strings.Split(strings.TrimSpace(traceparent), "-")
	if len(parts) == 4 && len(parts[0]) == 2 {
		if id := strings.ToLower(parts[1]); ValidTraceID(id) {
			return id
		}
	}
	return ""
}

// WithTrace stores id as the trace of ctx. If ctx already had a different trace it
// becomes parent_trace_id, so background work can be linked to what started it.
func WithTrace(ctx context.Context, id string) context.Context {
	if prev := TraceID(ctx); prev != "" && prev != id {
		ctx = context.WithValue(ctx, parentKey, prev)
	}
	return context.WithValue(ctx, traceKey, id)
}

// NewTrace starts a new trace on ctx (the old one, if any, becomes the parent).
func NewTrace(ctx context.Context) context.Context {
	return WithTrace(ctx, NewTraceID())
}

// TraceID returns the trace id carried by ctx, or "".
func TraceID(ctx context.Context) string {
	id, _ := ctx.Value(traceKey).(string)
	return id
}

// ParentTraceID returns the parent trace id carried by ctx, or "".
func ParentTraceID(ctx context.Context) string {
	id, _ := ctx.Value(parentKey).(string)
	return id
}

// WithAttrs attaches business fields (chain, job_id, block, ...) that every log line
// written with this ctx will carry.
func WithAttrs(ctx context.Context, attrs ...slog.Attr) context.Context {
	prev, _ := ctx.Value(attrsKey).([]slog.Attr)
	merged := make([]slog.Attr, 0, len(prev)+len(attrs))
	merged = append(merged, prev...)
	merged = append(merged, attrs...)
	return context.WithValue(ctx, attrsKey, merged)
}

func ctxAttrs(ctx context.Context) []slog.Attr {
	attrs, _ := ctx.Value(attrsKey).([]slog.Attr)
	return attrs
}
