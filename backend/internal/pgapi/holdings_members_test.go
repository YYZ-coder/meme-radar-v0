package pgapi_test

import (
	"testing"
	"time"

	"github.com/meme-radar/radar/internal/api"
	"github.com/meme-radar/radar/internal/lists"
	"github.com/meme-radar/radar/internal/pgapi"
)

// Queue/activate membership using H3's real API and compute-owned builder.
// No test creates or fills membership tables directly.
func queueHolderMembers(t *testing.T, f *fixture, csv string) api.ListVersionChange {
	t.Helper()
	h := f.handler()
	return applyH3(t, h, uploadH3(t, h, csv).ID)
}

func buildHolderMembers(t *testing.T, f *fixture) {
	t.Helper()
	if err := lists.New(f.st, nil).Step(t.Context(), f.now); err != nil {
		t.Fatal(err)
	}
}

func holderBackend(f *fixture) *pgapi.Backend {
	return pgapi.New(f.st, f.cfg, func() time.Time { return f.now })
}

// Active: developer + A; B is excluded; C exists on another chain and only in
// a pending BSC version. Hand-counted: exactly two BSC smart-money holders.
func smartLabelMembers(t *testing.T, f *fixture) {
	t.Helper()
	queueHolderMembers(t, f, "chain,address,label,tags,twitter\n"+
		"bsc,0x00000000000000000000000000000000000000ee,Developer,,,\n"+
		"bsc,0x0000000000000000000000000000000000000011,Alpha,,,\n"+
		"bsc,0x0000000000000000000000000000000000000012,Excluded,,,\n"+
		"base,0x0000000000000000000000000000000000000013,Other chain,,,\n")
	buildHolderMembers(t, f)
	if _, err := holderBackend(f).Exclude(t.Context(), api.BSC, "0x0000000000000000000000000000000000000012", "fixture exclusion"); err != nil {
		t.Fatal(err)
	}
	buildHolderMembers(t, f)
	queueHolderMembers(t, f, "chain,address,label,tags,twitter\nbsc,0x0000000000000000000000000000000000000013,Pending,,,\n")
	if version, err := holderBackend(f).ActiveListVersion(t.Context()); err != nil || version != "3" {
		t.Fatalf("active version=%s,%v; want 3", version, err)
	}
}
