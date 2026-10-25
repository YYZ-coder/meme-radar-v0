// Package mockdata is a deterministic in-memory Backend for frontend development.
//
// It mirrors frontend/src/mock so the browser smoke tests pass against the Go API:
// the same seeded token universe, sample tokens, admin state and list-build timing.
package mockdata

import (
	"sync"
	"time"

	"github.com/meme-radar/radar/internal/api"
)

// Data is the mock backend. It is safe for concurrent use.
type Data struct {
	now func() time.Time

	mu         sync.Mutex
	members    []api.Member
	exclusions []api.Exclusion
	active     int // active smart-money list version
	tail       int // latest accepted (possibly still building) version
	builds     map[int]*build
	imports    map[string]*importEntry
	failures   []api.ParseFailure
}

// New returns mock data whose time-dependent values follow now.
func New(now func() time.Time) *Data {
	if now == nil {
		now = time.Now
	}
	return &Data{
		now: now, members: seedMembers(), exclusions: []api.Exclusion{}, active: 42, tail: 42,
		builds: map[int]*build{}, imports: map[string]*importEntry{}, failures: seedFailures(),
	}
}

func (d *Data) ms() float64 { return float64(d.now().UnixMilli()) }
