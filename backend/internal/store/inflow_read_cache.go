package store

import (
	"container/list"
	"sync"
)

const (
	inflowVersionLimit   = 32
	inflowSelectionLimit = 50000
	inflowPayloadLimit   = 2000
)

type inflowVersionKey struct {
	version int64
	native  bool
}
type inflowVersionEntry struct {
	key      inflowVersionKey
	selected bool
	rows     []InflowDataRow
	payloads map[string]InflowDataRow
}

// Only immutable published values are cached. Every new query still reads the
// active membership and current heads in PostgreSQL; a cache entry is never a
// source of current version IDs or readiness. Returned values are immutable.
type inflowReadCache struct {
	mu            sync.Mutex
	entries       map[inflowVersionKey]*list.Element
	order         list.List
	selectionRows int
}

func (c *inflowReadCache) entryLocked(key inflowVersionKey) *inflowVersionEntry {
	if c.entries == nil {
		c.entries = map[inflowVersionKey]*list.Element{}
	}
	if e := c.entries[key]; e != nil {
		c.order.MoveToFront(e)
		return e.Value.(*inflowVersionEntry)
	}
	v := &inflowVersionEntry{key: key, payloads: map[string]InflowDataRow{}}
	c.entries[key] = c.order.PushFront(v)
	return v
}

func (c *inflowReadCache) evictLocked() {
	for c.order.Len() > inflowVersionLimit || c.selectionRows > inflowSelectionLimit {
		e := c.order.Back()
		v := c.order.Remove(e).(*inflowVersionEntry)
		c.selectionRows -= len(v.rows)
		delete(c.entries, v.key)
	}
}

func (c *inflowReadCache) selections(native bool) (map[int64][]InflowDataRow, []int64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	rows := map[int64][]InflowDataRow{}
	versions := []int64{}
	for key, e := range c.entries {
		v := e.Value.(*inflowVersionEntry)
		if key.native == native && v.selected {
			rows[key.version] = v.rows
			versions = append(versions, key.version)
		}
	}
	return rows, versions
}

func (c *inflowReadCache) putSelection(version int64, native bool, rows []InflowDataRow, complete bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	v := c.entryLocked(inflowVersionKey{version, native})
	c.selectionRows += len(rows) - len(v.rows)
	v.rows = rows
	v.selected = true
	if complete {
		for _, row := range rows {
			if len(v.payloads) < inflowPayloadLimit {
				v.payloads[string(row.Address)] = row
			}
		}
	}
	c.evictLocked()
}

func (c *inflowReadCache) payload(version int64, native bool, address []byte) (InflowDataRow, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e := c.entries[inflowVersionKey{version, native}]
	if e == nil {
		return InflowDataRow{}, false
	}
	c.order.MoveToFront(e)
	row, ok := e.Value.(*inflowVersionEntry).payloads[string(address)]
	return row, ok
}

func (c *inflowReadCache) putPayload(version int64, native bool, row InflowDataRow) {
	c.mu.Lock()
	defer c.mu.Unlock()
	v := c.entryLocked(inflowVersionKey{version, native})
	if len(v.payloads) < inflowPayloadLimit {
		v.payloads[string(row.Address)] = row
	}
	c.evictLocked()
}
