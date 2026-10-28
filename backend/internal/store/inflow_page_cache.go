package store

import (
	"container/list"
	"sync"
)

type inflowPageShape struct {
	chain, window string
	native        bool
	limit         int
}

type inflowPageEntry struct {
	key   inflowPageShape
	value InflowPagedSnapshot
}

// Only complete immutable first pages enter this32-entry LRU. Current heads,
// active membership and readiness always come from the new PostgreSQL batch.
// At most100 rows per entry are retained; returned values remain immutable.
type inflowPageReadCache struct {
	mu    sync.Mutex
	order list.List
	byKey map[inflowPageShape]*list.Element
}

func (c *inflowPageReadCache) get(key inflowPageShape) (InflowPagedSnapshot, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e := c.byKey[key]
	if e == nil {
		return InflowPagedSnapshot{}, false
	}
	c.order.MoveToFront(e)
	return e.Value.(inflowPageEntry).value, true
}

func (c *inflowPageReadCache) put(key inflowPageShape, value InflowPagedSnapshot) {
	if len(value.Rows) > 100 || len(value.Rows) != min(key.limit, min(value.Count, 2000)) {
		return
	}
	value.Rows = append([]InflowDataRow{}, value.Rows...)
	value.Heads = append([]InflowHead{}, value.Heads...)
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.byKey == nil {
		c.byKey = map[inflowPageShape]*list.Element{}
	}
	if e := c.byKey[key]; e != nil {
		e.Value = inflowPageEntry{key, value}
		c.order.MoveToFront(e)
	} else {
		c.byKey[key] = c.order.PushFront(inflowPageEntry{key, value})
	}
	for c.order.Len() > 32 {
		v := c.order.Remove(c.order.Back()).(inflowPageEntry)
		delete(c.byKey, v.key)
	}
}

func sameInflowPageHeads(a, b []InflowHead) bool {
	if len(a) != len(b) {
		return false
	}
	for _, x := range a {
		found := false
		for _, y := range b {
			if x.Chain == y.Chain && x.Window == y.Window && x.Version == y.Version {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}
