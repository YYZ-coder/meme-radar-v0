package httpapi

import (
	"context"
	"net/http"
	"strconv"
	"sync/atomic"
	"time"
)

type dataTimeKey struct{}
type dataObservation struct {
	at     time.Time
	maxAge time.Duration
}
type dataTime struct {
	observation atomic.Pointer[dataObservation]
}

// RecordDataRead records when a successful observation began. Handlers retain
// that time on cache hits and reject it after maxAge instead of changing it to now.
func RecordDataRead(ctx context.Context, at time.Time, maxAge time.Duration) {
	if stamp, ok := ctx.Value(dataTimeKey{}).(*dataTime); ok {
		stamp.observation.Store(&dataObservation{at, maxAge})
	}
}

func writeDataTime(w http.ResponseWriter, r *http.Request) error {
	stamp, ok := r.Context().Value(dataTimeKey{}).(*dataTime)
	if !ok {
		return nil
	}
	observed := stamp.observation.Load()
	if observed == nil {
		return nil
	}
	age := time.Since(observed.at)
	if observed.maxAge > 0 && age > observed.maxAge {
		return context.DeadlineExceeded
	}
	if age < 0 {
		age = 0
	}
	w.Header().Set("X-Data-As-Of", observed.at.UTC().Format(time.RFC3339Nano))
	w.Header().Set("X-Data-Age-Ms", strconv.FormatInt(age.Milliseconds(), 10))
	return nil
}
