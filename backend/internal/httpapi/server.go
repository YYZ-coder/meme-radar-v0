// Package httpapi implements the REST API server.
package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/meme-radar/radar/internal/obs"
)

// Pinger is satisfied by *sql.DB and allows the health handler to check
// database connectivity without depending on the concrete sql.DB type.
type Pinger interface {
	PingContext(ctx context.Context) error
}

// Server is the HTTP API server. It implements http.Handler.
type Server struct {
	addr    string
	pinger  Pinger
	mux     *http.ServeMux
	handler http.Handler // mux wrapped with trace, recovery and access log
}

// NewServer creates a Server listening on addr.
// pinger is typically a *sql.DB, used by the health endpoint.
func NewServer(addr string, pinger Pinger) *Server {
	s := &Server{
		addr:   addr,
		pinger: pinger,
		mux:    http.NewServeMux(),
	}
	s.registerRoutes()
	s.handler = withObservability(s.mux)
	return s
}

// Handle registers an API route (Go 1.22 pattern, e.g. "GET /api/v1/search").
func (s *Server) Handle(pattern string, h HandlerFunc) {
	s.mux.Handle(pattern, adapt(h))
}

// ServeHTTP implements http.Handler so the server can be used in tests
// without starting a real listener.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.handler.ServeHTTP(w, r)
}

// Run starts the HTTP listener and blocks until ctx is cancelled or the
// listener fails. It performs a graceful shutdown with a 10-second deadline.
func (s *Server) Run(ctx context.Context) error {
	srv := &http.Server{
		Addr:              s.addr,
		Handler:           s.handler,
		ReadHeaderTimeout: 10 * time.Second,
	}

	errCh := make(chan error, 1)
	obs.Go(ctx, "http.listen", func(context.Context) error {
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			errCh <- fmt.Errorf("listen: %w", err)
		}
		close(errCh)
		return nil
	})

	select {
	case <-ctx.Done():
		shutCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		return srv.Shutdown(shutCtx)
	case err := <-errCh:
		return err
	}
}

func (s *Server) registerRoutes() {
	s.mux.HandleFunc("GET /healthz", s.handleHealthz)
	// Anything unmatched gets the contract's JSON 404 instead of the mux's text body.
	s.Handle("/", func(http.ResponseWriter, *http.Request) error {
		return obs.New(obs.CodeNotFound, "no such endpoint")
	})
}

type healthResponse struct {
	Status string `json:"status"`
	DB     string `json:"db,omitempty"`
}

func (s *Server) handleHealthz(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()

	w.Header().Set("Content-Type", "application/json")

	if err := s.pinger.PingContext(ctx); err != nil {
		slog.WarnContext(ctx, "database ping failed",
			slog.String(obs.KeyCode, string(obs.RuntimeDBUnavailable)), slog.String(obs.KeyErr, err.Error()))
		w.WriteHeader(http.StatusServiceUnavailable)
		_ = json.NewEncoder(w).Encode(healthResponse{Status: "degraded", DB: "unreachable"})
		return
	}

	_ = json.NewEncoder(w).Encode(healthResponse{Status: "ok"})
}
