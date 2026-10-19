package obs

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"gopkg.in/yaml.v3"
)

type syncBuf struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuf) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuf) lines(t *testing.T) []map[string]any {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []map[string]any
	for _, line := range strings.Split(strings.TrimSpace(s.b.String()), "\n") {
		if line == "" {
			continue
		}
		var m map[string]any
		if err := json.Unmarshal([]byte(line), &m); err != nil {
			t.Fatalf("log line is not JSON: %q", line)
		}
		out = append(out, m)
	}
	return out
}

func capture(t *testing.T) *syncBuf {
	t.Helper()
	buf := &syncBuf{}
	prev := slog.Default()
	slog.SetDefault(NewLogger(buf, LogOptions{Level: "debug", Service: "radar", Role: "test", Version: "t1"}))
	t.Cleanup(func() { slog.SetDefault(prev) })
	return buf
}

func TestTraceIDs(t *testing.T) {
	id := NewTraceID()
	if !ValidTraceID(id) {
		t.Fatalf("invalid generated id %q", id)
	}
	cases := []struct{ x, tp, want string }{
		{"4BF92F3577B34DA6A3CE929D0E0E4736", "", "4bf92f3577b34da6a3ce929d0e0e4736"},
		{"", "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01", "4bf92f3577b34da6a3ce929d0e0e4736"},
		{"not-hex", "00-00000000000000000000000000000000-00f067aa0ba902b7-01", ""},
		{"", "garbage", ""},
	}
	for _, c := range cases {
		if got := ParseTraceID(c.x, c.tp); got != c.want {
			t.Errorf("ParseTraceID(%q,%q)=%q want %q", c.x, c.tp, got, c.want)
		}
	}
	parent := WithTrace(context.Background(), id)
	child := NewTrace(parent)
	if ParentTraceID(child) != id || TraceID(child) == id {
		t.Fatal("child trace should keep parent id")
	}
}

func TestLoggerFieldsAndRedaction(t *testing.T) {
	buf := capture(t)
	ctx := WithAttrs(WithTrace(context.Background(), "4bf92f3577b34da6a3ce929d0e0e4736"), slog.String(KeyChain, "solana"))
	slog.InfoContext(ctx, "hello",
		slog.String("authorization", "Bearer abc"), slog.String("admin_token", "x"),
		slog.String("private_key", "y"), slog.String(KeyToken, "GX1J"))
	l := buf.lines(t)[0]
	for k, want := range map[string]any{
		"service": "radar", "role": "test", "version": "t1", KeyTraceID: "4bf92f3577b34da6a3ce929d0e0e4736",
		KeyChain: "solana", "authorization": redactedText, "admin_token": redactedText,
		"private_key": redactedText, KeyToken: "GX1J",
	} {
		if l[k] != want {
			t.Errorf("%s = %v, want %v", k, l[k], want)
		}
	}
}

func TestGoRecoversPanicAndProcessSurvives(t *testing.T) {
	buf := capture(t)
	before := PanicCount()
	err := <-Go(context.Background(), "boom", func(context.Context) error { panic("kaboom") })
	if As(err).Code != CodeInternal || PanicCount() != before+1 {
		t.Fatalf("panic not converted: %v", err)
	}
	var found bool
	for _, l := range buf.lines(t) {
		if l[KeyCode] == string(RuntimePanicRecovered) {
			found = l[KeyTraceID] != nil && strings.Contains(l[KeyStack].(string), "obs_test.go")
		}
	}
	if !found {
		t.Fatal("panic log missing trace_id or stack")
	}
}

func TestLoopIterationsAreIsolated(t *testing.T) {
	capture(t)
	ctx, cancel := context.WithCancel(context.Background())
	var mu sync.Mutex
	var traces []string
	n := 0
	done := Loop(ctx, "loop", time.Millisecond, func(ctx context.Context) error {
		mu.Lock()
		defer mu.Unlock()
		n++
		traces = append(traces, TraceID(ctx))
		if n == 1 {
			panic("first iteration panics")
		}
		if n == 3 {
			cancel()
		}
		return nil
	})
	<-done
	if n < 3 || traces[0] == traces[1] {
		t.Fatalf("loop should survive panic with fresh traces: n=%d traces=%v", n, traces)
	}
}

func TestSuperviseRestarts(t *testing.T) {
	buf := capture(t)
	ctx, cancel := context.WithCancel(context.Background())
	runs := 0
	done := superviseWith(ctx, "worker", time.Millisecond, 2*time.Millisecond, func(context.Context) error {
		runs++
		if runs == 3 {
			cancel()
			return nil
		}
		if runs == 1 {
			panic("crash")
		}
		return errors.New("transient")
	})
	<-done
	restarts := 0
	for _, l := range buf.lines(t) {
		if l[KeyCode] == string(RuntimeGoroutineRestarted) {
			restarts++
		}
	}
	if runs != 3 || restarts != 2 {
		t.Fatalf("runs=%d restarts=%d", runs, restarts)
	}
}

func TestErrorHelpers(t *testing.T) {
	e := As(Wrap(CodeBusy, "slow", errors.New("timeout")))
	if e.Code != CodeBusy || e.Code.Status() != 503 || !errors.Is(e, e.Err) {
		t.Fatal("wrap/as broken")
	}
	if As(errors.New("x")).Code != CodeInternal || ParseDecodeFailed.Status() != 500 || ParseDecodeFailed.IsAPI() {
		t.Fatal("internal codes must map to 500 and not be API codes")
	}
	for code := range Catalog {
		s := string(code)
		if strings.Contains(s, ".") == code.IsAPI() {
			t.Errorf("%s: API codes are lower_snake without dots, internal codes are area.reason", code)
		}
	}
}

// API codes must equal api/openapi.yaml ErrorCode enum (contract test).
func TestAPICodesMatchOpenAPI(t *testing.T) {
	raw, err := os.ReadFile("../../../api/openapi.yaml")
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Components struct {
			Schemas map[string]struct {
				Enum []string `yaml:"enum"`
			} `yaml:"schemas"`
		} `yaml:"components"`
	}
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	want := doc.Components.Schemas["ErrorCode"].Enum
	var got []string
	for code := range Catalog {
		if code.IsAPI() {
			got = append(got, string(code))
		}
	}
	sort.Strings(want)
	sort.Strings(got)
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("Go API codes %v != openapi ErrorCode %v", got, want)
	}
}
