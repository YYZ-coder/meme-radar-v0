package httpapi

import (
	"context"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/meme-radar/radar/internal/api"
	"github.com/meme-radar/radar/internal/smlist"
)

var uuidRe = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[1-8][0-9a-fA-F]{3}-[89abAB][0-9a-fA-F]{3}-[0-9a-fA-F]{12}$`)

type idemEntry struct{ fingerprint, version string }

// idempotency remembers which list version each Idempotency-Key produced.
type idempotency struct {
	mu    sync.Mutex
	byKey map[string]idemEntry
}

// idempotent executes change once per Idempotency-Key. A replay with the same fingerprint returns the
// current view of the original version; a different request under the key is 400.
func (s *server) idempotent(r *http.Request, fingerprint string, change func() (api.ListVersionChange, error)) (api.ListVersionChange, error) {
	key := r.Header.Get("Idempotency-Key")
	if !uuidRe.MatchString(key) {
		return api.ListVersionChange{}, badParam("Idempotency-Key", "a UUID Idempotency-Key header is required")
	}
	s.idem.mu.Lock()
	defer s.idem.mu.Unlock()
	if prev, ok := s.idem.byKey[key]; ok {
		if prev.fingerprint != fingerprint {
			return api.ListVersionChange{}, badParam("Idempotency-Key", "key already identifies a different request")
		}
		return s.b.ListChange(r.Context(), prev.version)
	}
	c, err := change()
	if err == nil {
		s.idem.byKey[key] = idemEntry{fingerprint, c.ListVersion}
	}
	return c, err
}

func (s *server) members(w http.ResponseWriter, r *http.Request) {
	chain, err := queryChain(r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	source, err := oneOf(r, "source", "all", "all", "import", "auto")
	if err != nil {
		s.fail(w, r, err)
		return
	}
	limit, err := queryInt(r, "limit", 50, 1, 100)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	q := r.URL.Query()
	m, err := s.b.Members(r.Context(), chain, source, strings.TrimSpace(q.Get("tag")), strings.TrimSpace(q.Get("q")), q.Get("cursor"), limit)
	s.reply(w, r, http.StatusOK, m, err)
}

func (s *server) exclude(w http.ResponseWriter, r *http.Request) {
	var body api.ExclusionRequest
	if err := decodeJSON(r, &body); err != nil {
		s.fail(w, r, err)
		return
	}
	reason := strings.TrimSpace(body.Reason)
	switch {
	case !body.Chain.Valid():
		s.fail(w, r, badParam("chain", "unknown chain"))
	case !smlist.ValidAddress(body.Chain, body.Address):
		s.fail(w, r, badParam("address", "malformed address"))
	case reason == "" || utf8.RuneCountInString(reason) > 200:
		s.fail(w, r, badParam("reason", "reason must be 1-200 characters"))
	default:
		fp := "POST exclusions " + smlist.ID(string(body.Chain), body.Address) + " " + reason
		c, err := s.idempotent(r, fp, func() (api.ListVersionChange, error) {
			return s.b.Exclude(r.Context(), body.Chain, body.Address, reason)
		})
		s.accepted(w, r, c, err)
	}
}

// walletChange handles DELETE …/{chain}/{address} list changes.
func (s *server) walletChange(change func(context.Context, api.Chain, string) (api.ListVersionChange, error)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		chain, address := api.Chain(r.PathValue("chain")), r.PathValue("address")
		if !smlist.ValidAddress(chain, address) {
			s.fail(w, r, badParam("address", "unknown chain or malformed address"))
			return
		}
		fp := r.Method + " " + r.URL.Path
		c, err := s.idempotent(r, fp, func() (api.ListVersionChange, error) { return change(r.Context(), chain, address) })
		s.accepted(w, r, c, err)
	}
}
