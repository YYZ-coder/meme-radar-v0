package httpapi

import (
	"net/http"
	"strconv"

	"github.com/meme-radar/radar/internal/api"
)

func badParam(field, message string) *api.Error {
	return api.Errorf(http.StatusBadRequest, "invalid_param", field, message)
}

// queryInt reads an optional integer within [lo, hi].
func queryInt(r *http.Request, name string, def, lo, hi int) (int, error) {
	raw := r.URL.Query().Get(name)
	if raw == "" {
		return def, nil
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < lo || n > hi {
		return 0, badParam(name, name+" must be an integer in ["+strconv.Itoa(lo)+", "+strconv.Itoa(hi)+"]")
	}
	return n, nil
}

// queryChain reads an optional chain filter; empty means all chains.
func queryChain(r *http.Request) (api.Chain, error) {
	c := api.Chain(r.URL.Query().Get("chain"))
	if c != "" && !c.Valid() {
		return "", badParam("chain", "unknown chain")
	}
	return c, nil
}

// Windows are the inflow time windows in display order.
var windows = []string{"1m", "5m", "1h", "4h", "24h"}

func validWindow(w string) bool {
	for _, x := range windows {
		if x == w {
			return true
		}
	}
	return false
}

// queryWindow reads the window parameter, defaulting to 1h.
func queryWindow(r *http.Request) (string, error) {
	w := r.URL.Query().Get("window")
	if w == "" {
		return "1h", nil
	}
	if !validWindow(w) {
		return "", badParam("window", "window must be one of 1m, 5m, 1h, 4h, 24h")
	}
	return w, nil
}
