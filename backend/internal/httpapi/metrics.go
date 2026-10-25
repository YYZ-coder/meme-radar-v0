package httpapi

import "net/http"

type responseStats struct {
	http.ResponseWriter
	status int
}

func (w *responseStats) WriteHeader(status int) {
	if status < 200 {
		w.ResponseWriter.WriteHeader(status)
		return
	}
	if w.status != 0 {
		return
	}
	w.status = status
	w.ResponseWriter.WriteHeader(status)
}

func (w *responseStats) Write(p []byte) (int, error) {
	if w.status == 0 {
		w.WriteHeader(http.StatusOK)
	}
	return w.ResponseWriter.Write(p)
}

// Unwrap keeps http.ResponseController operations available to the handler.
func (w *responseStats) Unwrap() http.ResponseWriter { return w.ResponseWriter }
