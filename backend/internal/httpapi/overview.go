package httpapi

import (
	"net/http"

	"github.com/meme-radar/radar/internal/api"
)

// chainWindow reads the chain filter and window shared by overview and facets.
func chainWindow(r *http.Request) (api.Chain, string, error) {
	c, err := queryChain(r)
	if err != nil {
		return "", "", err
	}
	w, err := queryWindow(r)
	return c, w, err
}

func (s *server) overview(w http.ResponseWriter, r *http.Request) {
	chain, window, err := chainWindow(r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	v, err := s.b.Overview(r.Context(), chain, window)
	s.reply(w, r, http.StatusOK, v, err)
}

func (s *server) facets(w http.ResponseWriter, r *http.Request) {
	chain, window, err := chainWindow(r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	v, err := s.b.Facets(r.Context(), chain, window)
	s.reply(w, r, http.StatusOK, v, err)
}
