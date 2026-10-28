package pgapi

import (
	"context"
	"encoding/json"
	"sync"
	"time"
)

type readEntry struct {
	data                             []byte
	observedAt, lastUse, attemptedAt time.Time
	fetching, pinned                 bool
	finished                         chan struct{}
	err                              error
}

type freshCache[K comparable, V any] struct {
	ctx        context.Context
	cancel     context.CancelFunc
	mu         sync.Mutex
	entries    map[K]*readEntry
	maxAge     time.Duration
	maxEntries int
	slots      chan struct{}
	fetch      func(context.Context, K) (V, time.Time, error)
	workers    sync.WaitGroup
	closed     bool
}

func newFreshCache[K comparable, V any](maxAge time.Duration, maxEntries int, slots chan struct{}, fetch func(context.Context, K) (V, time.Time, error)) *freshCache[K, V] {
	ctx, cancel := context.WithCancel(context.Background())
	c := &freshCache[K, V]{ctx: ctx, cancel: cancel, entries: map[K]*readEntry{}, maxAge: maxAge, maxEntries: maxEntries, slots: slots, fetch: fetch}
	c.workers.Add(1)
	go c.refreshLoop()
	return c
}

func (c *freshCache[K, V]) fresh(e *readEntry, now time.Time) bool {
	// Leave room for the API-to-proxy and proxy-to-caller handoff. The
	// timestamp stays original and both HTTP boundaries enforce the hard cap.
	return len(e.data) > 0 && now.Sub(e.observedAt) < c.maxAge-min(20*time.Millisecond, c.maxAge/10)
}

func (c *freshCache[K, V]) get(ctx context.Context, key K) (V, time.Time, error) {
	var zero V
	now := time.Now()
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return zero, time.Time{}, context.Canceled
	}
	e := c.entries[key]
	if e == nil {
		if len(c.entries) >= c.maxEntries {
			var oldest K
			found := false
			var used time.Time
			for k, v := range c.entries {
				if !v.fetching && (!found || v.lastUse.Before(used)) {
					oldest, used, found = k, v.lastUse, true
				}
			}
			if !found {
				c.mu.Unlock()
				return zero, time.Time{}, context.DeadlineExceeded
			}
			delete(c.entries, oldest)
		}
		e = &readEntry{}
		c.entries[key] = e
	}
	e.lastUse = now
	if c.fresh(e, now) {
		data, at := e.data, e.observedAt
		c.mu.Unlock()
		err := json.Unmarshal(data, &zero)
		return zero, at, err
	}
	if !e.fetching {
		c.launch(key, e)
	}
	finished := e.finished
	c.mu.Unlock()
	select {
	case <-ctx.Done():
		return zero, time.Time{}, ctx.Err()
	case <-finished:
	}
	c.mu.Lock()
	data, at, err, fresh := e.data, e.observedAt, e.err, c.fresh(e, time.Now())
	c.mu.Unlock()
	if !fresh {
		if err == nil {
			err = context.DeadlineExceeded
		}
		return zero, at, err
	}
	err = json.Unmarshal(data, &zero)
	return zero, at, err
}

// launch is called under mu. All callers join the same in-flight entry. A
// canceled caller does not cancel a shared read; Close cancels every worker.
func (c *freshCache[K, V]) launch(key K, e *readEntry) {
	e.fetching = true
	e.finished = make(chan struct{})
	e.attemptedAt = time.Now()
	c.workers.Add(1)
	go func() {
		defer c.workers.Done()
		var data []byte
		var at time.Time
		var err error
		select {
		case c.slots <- struct{}{}:
			ctx, cancel := context.WithTimeout(c.ctx, 2*time.Second)
			var value V
			value, at, err = c.fetch(ctx, key)
			cancel()
			<-c.slots
			if err == nil {
				data, err = json.Marshal(value)
			}
			if err == nil && (at.IsZero() || time.Since(at) >= c.maxAge) {
				err = context.DeadlineExceeded
			}
		case <-c.ctx.Done():
			err = c.ctx.Err()
		}
		c.mu.Lock()
		if err == nil {
			e.data, e.observedAt = data, at
		}
		e.err = err
		e.fetching = false
		close(e.finished)
		c.mu.Unlock()
	}()
}

// pin keeps the singleton operational report current even without HTTP traffic.
func (c *freshCache[K, V]) pin(ctx context.Context, key K) error {
	if _, _, err := c.get(ctx, key); err != nil {
		return err
	}
	c.mu.Lock()
	if e := c.entries[key]; e != nil {
		e.pinned = true
	}
	c.mu.Unlock()
	return nil
}

func (c *freshCache[K, V]) refreshLoop() {
	defer c.workers.Done()
	refreshAfter := c.maxAge / 3
	ticker := time.NewTicker(max(time.Millisecond, c.maxAge/4))
	defer ticker.Stop()
	for {
		select {
		case <-c.ctx.Done():
			return
		case now := <-ticker.C:
			c.mu.Lock()
			if !c.closed {
				for key, e := range c.entries {
					if !e.fetching && (e.pinned || now.Sub(e.lastUse) < 3*c.maxAge) && now.Sub(e.attemptedAt) >= refreshAfter {
						c.launch(key, e)
					}
				}
			}
			c.mu.Unlock()
		}
	}
}

func (c *freshCache[K, V]) close() {
	c.mu.Lock()
	c.closed = true
	c.cancel()
	c.mu.Unlock()
	c.workers.Wait()
}
