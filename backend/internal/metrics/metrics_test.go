package metrics_test

import (
	"io"
	"net"
	"net/http"
	"strings"
	"testing"

	"github.com/meme-radar/radar/internal/metrics"
)

func TestServeExposesRadarMetrics(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := l.Addr().String()
	l.Close()
	stop := metrics.Serve(addr, nil)
	defer stop()
	metrics.BlocksCommitted.WithLabelValues("solana", "forward").Add(2)
	resp, err := http.Get("http://" + addr + "/metrics")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(b), `radar_blocks_committed_total{chain="solana",role="forward"} 2`) {
		t.Errorf("/metrics lacks the committed-blocks counter:\n%.400s", b)
	}
	metrics.Serve("off", nil)() // disabled: nothing to stop
}
