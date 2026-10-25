package httpapi

import (
	"net/http"
	"strings"
	"unicode/utf8"
)

func (s *server) nativePrices(w http.ResponseWriter, r *http.Request) {
	v, err := s.b.NativePrices(r.Context())
	s.reply(w, r, http.StatusOK, v, err)
}

func (s *server) search(w http.ResponseWriter, r *http.Request) {
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	if q == "" || utf8.RuneCountInString(q) > 128 {
		s.fail(w, r, badParam("q", "q must be 1-128 characters"))
		return
	}
	chain, err := queryChain(r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	limit, err := queryInt(r, "limit", 10, 1, 20)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	v, err := s.b.Search(r.Context(), q, chain, limit)
	s.reply(w, r, http.StatusOK, v, err)
}
