package httpapi

import (
	"encoding/json"
	"net/http"

	"github.com/meme-radar/radar/internal/api"
)

const maxJSONBody = 64 << 10

func decodeJSON(r *http.Request, v any) error {
	if err := json.NewDecoder(http.MaxBytesReader(nil, r.Body, maxJSONBody)).Decode(v); err != nil {
		return badParam("", "request body must be valid JSON")
	}
	return nil
}

func (s *server) batch(w http.ResponseWriter, r *http.Request) {
	var body api.BatchRequest
	if err := decodeJSON(r, &body); err != nil {
		s.fail(w, r, err)
		return
	}
	if len(body.Tokens) == 0 || len(body.Tokens) > 100 {
		s.fail(w, r, badParam("tokens", "1-100 tokens required"))
		return
	}
	for _, k := range body.Tokens {
		if !k.Chain.Valid() || k.Address == "" {
			s.fail(w, r, badParam("tokens", "each token needs a supported chain and an address"))
			return
		}
	}
	if !validWindow(body.Window) {
		s.fail(w, r, badParam("window", "window must be one of 1m, 5m, 1h, 4h, 24h"))
		return
	}
	native, err := queryCurrency(body.Currency)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	active, err := s.b.ActiveListVersion(r.Context())
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if body.QueryID != "" || body.ListVersion != "" {
		if body.QueryID == "" || body.ListVersion == "" {
			s.fail(w, r, badParam("query_id", "query_id and list_version must be sent together"))
			return
		}
		snap := s.snaps.get(body.QueryID, s.opt.Now())
		if snap == nil {
			s.fail(w, r, expired("query snapshot expired or unknown; re-query"))
			return
		}
		if snap.listVersion != body.ListVersion || body.ListVersion != active {
			s.fail(w, r, expired("smart-money list changed; pause refresh and re-query"))
			return
		}
		if snap.window != body.Window || snap.native != native {
			s.fail(w, r, badParam("window", "window or currency differs from the snapshot"))
			return
		}
	}
	rows, err := s.b.InflowRows(r.Context(), body.Window, native, body.Tokens)
	s.reply(w, r, http.StatusOK, api.Batch{AsOf: s.opt.Now().UTC().Format("2006-01-02T15:04:05.000Z"), ListVersion: active, Items: rows}, err)
}
