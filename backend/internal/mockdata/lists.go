package mockdata

import (
	"context"
	"net/http"
	"slices"
	"strconv"
	"strings"

	"github.com/meme-radar/radar/internal/api"
	"github.com/meme-radar/radar/internal/smlist"
)

type draft struct {
	members    []api.Member
	exclusions []api.Exclusion
}

func (dr draft) clone() draft {
	out := draft{members: make([]api.Member, len(dr.members)), exclusions: slices.Clone(dr.exclusions)}
	for i, m := range dr.members {
		m.Tags, m.Sources = slices.Clone(m.Tags), slices.Clone(m.Sources)
		out.members[i] = m
	}
	return out
}

type build struct {
	version, parent int
	state           string // queued, running, ready, activated, failed
	counts          api.ChangeCounts
	createdAt       float64
	readyAt         float64
	activatedAt     *float64
	errorCode       *string
	draft           draft
}

type importEntry struct {
	report api.ImportReport
	all    []api.ImportRow
}

// tickLocked advances queued builds like the TS mock's fake worker: running after
// 0.5s, activated 2s after creation, only on top of the active version.
func (d *Data) tickLocked(now float64) {
	for v := d.active + 1; ; v++ {
		b, ok := d.builds[v]
		if !ok || b.state == "failed" || b.parent != d.active {
			return
		}
		if now < b.readyAt {
			if now >= b.readyAt-1500 {
				b.state = "running"
			}
			return
		}
		d.activateLocked(b, now)
	}
}

func (d *Data) activateLocked(b *build, now float64) {
	dr := b.draft.clone()
	d.members, d.exclusions = dr.members, dr.exclusions
	d.active = b.version
	b.state = "activated"
	b.activatedAt = &now
}

// lock acquires the state lock after advancing builds to now.
func (d *Data) lock() float64 {
	now := d.ms()
	d.mu.Lock()
	d.tickLocked(now)
	return now
}

func (d *Data) tailDraftLocked() draft {
	if b, ok := d.builds[d.tail]; ok {
		return b.draft.clone()
	}
	return draft{members: d.members, exclusions: d.exclusions}.clone()
}

// queueLocked creates the next version from the latest accepted draft.
func (d *Data) queueLocked(now float64, mutate func(*draft) (api.ChangeCounts, error)) (api.ListVersionChange, error) {
	dr := d.tailDraftLocked()
	counts, err := mutate(&dr)
	if err != nil {
		return api.ListVersionChange{}, err
	}
	b := &build{version: d.tail + 1, parent: d.tail, state: "queued", counts: counts, createdAt: now, readyAt: now + 2000, draft: dr}
	d.tail = b.version
	d.builds[b.version] = b
	return d.changeLocked(b), nil
}

func (d *Data) statusLocked(b *build) api.ListBuildStatus {
	active := strconv.Itoa(d.active)
	st := api.ListBuildStatus{
		ListVersion: strconv.Itoa(b.version), PreviousVersion: strconv.Itoa(b.parent), ActiveVersion: active,
		Status: "building", BuildStatus: b.state, CreatedAt: iso(b.createdAt), ErrorCode: b.errorCode, ChangeCounts: b.counts,
	}
	if b.state == "activated" {
		st.Status = map[bool]string{true: "active", false: "retired"}[b.version == d.active]
	} else if b.parent != d.active {
		st.BlockingVersion = ptr(strconv.Itoa(b.parent))
	}
	if b.activatedAt != nil {
		st.ActivatedAt = ptr(iso(*b.activatedAt))
	}
	return st
}

func (d *Data) changeLocked(b *build) api.ListVersionChange {
	st := d.statusLocked(b)
	return api.ListVersionChange{ListVersion: st.ListVersion, PreviousVersion: st.PreviousVersion, ActiveVersion: st.ActiveVersion, Status: st.Status, BuildStatus: st.BuildStatus, ChangeCounts: st.ChangeCounts}
}

func (d *Data) isMemberLocked(chain api.Chain, address string) bool {
	id := smlist.ID(string(chain), address)
	return slices.ContainsFunc(d.members, func(m api.Member) bool { return smlist.ID(string(m.Chain), m.Address) == id })
}

func notFound(what string) *api.Error {
	return api.Errorf(http.StatusNotFound, "not_found", "", what+" not found")
}

// CreateImport validates an uploaded list without changing the active list.
func (d *Data) CreateImport(_ context.Context, fileName string, content []byte) (api.ImportReport, error) {
	now := d.lock()
	defer d.mu.Unlock()
	rows, err := smlist.Parse(fileName, content, d.isMemberLocked)
	if err != nil {
		return api.ImportReport{}, err
	}
	rep := api.ImportReport{
		ID: "imp_" + strconv.FormatUint(uint64(hash(fileName+":"+num(now)+":"+strconv.Itoa(len(d.imports)))), 16), FileName: fileName,
		CreatedAt: iso(now), ExpiresAt: iso(now + 30*60_000), Rows: []api.ImportRow{},
	}
	var invalid, rest []api.ImportRow
	for _, r := range rows {
		rep.Totals.Rows++
		switch r.Status {
		case "valid":
			rep.Totals.Valid++
		case "invalid":
			rep.Totals.Invalid++
		case "duplicate":
			rep.Totals.Duplicate++
		case "existing":
			rep.Totals.Existing++
		}
		if r.Status == "invalid" {
			invalid = append(invalid, r)
		} else {
			rest = append(rest, r)
		}
	}
	all := append(invalid, rest...)
	rep.Rows = all[:min(len(all), max(500, len(invalid)))]
	d.imports[rep.ID] = &importEntry{report: rep, all: rows}
	return rep, nil
}

// Import returns a stored preview; unapplied previews disappear when they expire.
func (d *Data) Import(_ context.Context, id string) (api.ImportReport, error) {
	now := d.lock()
	defer d.mu.Unlock()
	e, ok := d.imports[id]
	if !ok || (!e.report.Applied && expiredAt(e.report.ExpiresAt, now)) {
		return api.ImportReport{}, notFound("import")
	}
	return e.report, nil
}

func expiredAt(stamp string, now float64) bool {
	return iso(now) >= stamp
}

// ApplyImport queues a new list version with the preview's valid and existing rows.
func (d *Data) ApplyImport(_ context.Context, id string) (api.ListVersionChange, error) {
	now := d.lock()
	defer d.mu.Unlock()
	e, ok := d.imports[id]
	if !ok {
		return api.ListVersionChange{}, notFound("import")
	}
	if e.report.Applied || expiredAt(e.report.ExpiresAt, now) {
		return api.ListVersionChange{}, api.Errorf(http.StatusConflict, "import_closed", "", "import already applied or expired")
	}
	change, err := d.queueLocked(now, func(dr *draft) (api.ChangeCounts, error) {
		var c api.ChangeCounts
		for _, row := range e.all {
			if (row.Status != "valid" && row.Status != "existing") || row.Chain == nil || row.Address == nil {
				continue
			}
			chain, id := api.Chain(*row.Chain), smlist.ID(*row.Chain, *row.Address)
			i := slices.IndexFunc(dr.members, func(m api.Member) bool { return smlist.ID(string(m.Chain), m.Address) == id })
			if i >= 0 {
				m := &dr.members[i]
				if !slices.Contains(m.Sources, "import") {
					m.Sources = append(m.Sources, "import")
				}
				for _, t := range row.Tags {
					if !slices.Contains(m.Tags, t) {
						m.Tags = append(m.Tags, t)
					}
				}
				slices.Sort(m.Tags)
				if m.Label == nil {
					m.Label = row.Label
				}
				if m.Twitter == nil {
					m.Twitter = row.Twitter
				}
				c.Merged++
				continue
			}
			tags := slices.Clone(row.Tags)
			slices.Sort(tags)
			excluded := slices.ContainsFunc(dr.exclusions, func(x api.Exclusion) bool { return smlist.ID(string(x.Chain), x.Address) == id })
			dr.members = append([]api.Member{{Chain: chain, Address: *row.Address, Label: row.Label, Tags: tags, Twitter: row.Twitter, Sources: []string{"import"}, AddedAt: iso(now), Excluded: excluded}}, dr.members...)
			c.Added++
		}
		c.TotalMembers = int64(len(dr.members))
		return c, nil
	})
	if err != nil {
		return api.ListVersionChange{}, err
	}
	e.report.Applied, e.report.AppliedListVersion = true, &change.ListVersion
	return change, nil
}

func (d *Data) buildLocked(version string) (*build, error) {
	v, err := strconv.Atoi(version)
	b, ok := d.builds[v]
	if err != nil || !ok {
		return nil, notFound("list build")
	}
	return b, nil
}

// ListBuild reports the preparation state of a list version.
func (d *Data) ListBuild(_ context.Context, version string) (api.ListBuildStatus, error) {
	d.lock()
	defer d.mu.Unlock()
	b, err := d.buildLocked(version)
	if err != nil {
		return api.ListBuildStatus{}, err
	}
	return d.statusLocked(b), nil
}

// RetryListBuild re-queues a failed version; other states are returned unchanged.
func (d *Data) RetryListBuild(_ context.Context, version string) (api.ListBuildStatus, error) {
	now := d.lock()
	defer d.mu.Unlock()
	b, err := d.buildLocked(version)
	if err != nil {
		return api.ListBuildStatus{}, err
	}
	if b.state == "failed" {
		b.state, b.errorCode, b.readyAt = "queued", nil, now+2000
	}
	return d.statusLocked(b), nil
}

// Members lists the active version's members.
func (d *Data) Members(_ context.Context, chain api.Chain, source, tag, q, cursor string, limit int) (api.Members, error) {
	_, off, err := decodeCursor(cursor)
	if err != nil {
		return api.Members{}, err
	}
	d.lock()
	defer d.mu.Unlock()
	q = strings.ToLower(q)
	var list []api.Member
	for _, m := range d.members {
		label := ""
		if m.Label != nil {
			label = strings.ToLower(*m.Label)
		}
		if (chain == "" || m.Chain == chain) &&
			(source == "all" || slices.Contains(m.Sources, source)) &&
			(tag == "" || slices.Contains(m.Tags, tag)) &&
			(q == "" || strings.HasPrefix(strings.ToLower(m.Address), q) || strings.Contains(label, q)) {
			list = append(list, m)
		}
	}
	offset := int(off)
	out := api.Members{ListVersion: strconv.Itoa(d.active), Total: int64(len(list)), Items: []api.Member{}}
	if offset < len(list) {
		out.Items = slices.Clone(list[offset:min(offset+limit, len(list))])
	}
	if offset+limit < len(list) {
		out.NextCursor = encodeCursor(0, float64(offset+limit))
	}
	return out, nil
}

// Exclusions lists the active version's excluded wallets.
func (d *Data) Exclusions(_ context.Context, chain api.Chain) (api.Exclusions, error) {
	d.lock()
	defer d.mu.Unlock()
	out := api.Exclusions{ListVersion: strconv.Itoa(d.active), Items: []api.Exclusion{}}
	for _, e := range d.exclusions {
		if chain == "" || e.Chain == chain {
			out.Items = append(out.Items, e)
		}
	}
	return out, nil
}

func memberIndex(dr *draft, id string) int {
	return slices.IndexFunc(dr.members, func(m api.Member) bool { return smlist.ID(string(m.Chain), m.Address) == id })
}

// Exclude queues a version that keeps the record but drops the wallet from statistics (D125).
func (d *Data) Exclude(_ context.Context, chain api.Chain, address, reason string) (api.ListVersionChange, error) {
	now := d.lock()
	defer d.mu.Unlock()
	id := smlist.ID(string(chain), address)
	return d.queueLocked(now, func(dr *draft) (api.ChangeCounts, error) {
		if i := slices.IndexFunc(dr.exclusions, func(x api.Exclusion) bool { return smlist.ID(string(x.Chain), x.Address) == id }); i >= 0 {
			dr.exclusions[i].Reason = reason
		} else {
			dr.exclusions = append([]api.Exclusion{{Chain: chain, Address: address, Reason: reason, CreatedAt: iso(now)}}, dr.exclusions...)
		}
		c := api.ChangeCounts{TotalMembers: int64(len(dr.members))}
		if i := memberIndex(dr, id); i >= 0 {
			dr.members[i].Excluded = true
			c.Merged = 1
		}
		return c, nil
	})
}

// Restore queues a version that lifts an exclusion.
func (d *Data) Restore(_ context.Context, chain api.Chain, address string) (api.ListVersionChange, error) {
	now := d.lock()
	defer d.mu.Unlock()
	id := smlist.ID(string(chain), address)
	return d.queueLocked(now, func(dr *draft) (api.ChangeCounts, error) {
		i := slices.IndexFunc(dr.exclusions, func(x api.Exclusion) bool { return smlist.ID(string(x.Chain), x.Address) == id })
		if i < 0 {
			return api.ChangeCounts{}, notFound("exclusion")
		}
		dr.exclusions = slices.Delete(dr.exclusions, i, i+1)
		c := api.ChangeCounts{TotalMembers: int64(len(dr.members))}
		if j := memberIndex(dr, id); j >= 0 {
			dr.members[j].Excluded = false
			c.Merged = 1
		}
		return c, nil
	})
}

// RemoveMember drops the import source; a member left without sources is removed.
func (d *Data) RemoveMember(_ context.Context, chain api.Chain, address string) (api.ListVersionChange, error) {
	now := d.lock()
	defer d.mu.Unlock()
	id := smlist.ID(string(chain), address)
	return d.queueLocked(now, func(dr *draft) (api.ChangeCounts, error) {
		i := memberIndex(dr, id)
		if i < 0 || !slices.Contains(dr.members[i].Sources, "import") {
			return api.ChangeCounts{}, notFound("imported member")
		}
		m := &dr.members[i]
		m.Sources = slices.DeleteFunc(m.Sources, func(s string) bool { return s == "import" })
		if len(m.Sources) > 0 {
			return api.ChangeCounts{Merged: 1, TotalMembers: int64(len(dr.members))}, nil
		}
		dr.members = slices.Delete(dr.members, i, i+1)
		return api.ChangeCounts{Removed: 1, TotalMembers: int64(len(dr.members))}, nil
	})
}

// ListChange returns the current view of an accepted change, for idempotent replays.
func (d *Data) ListChange(_ context.Context, version string) (api.ListVersionChange, error) {
	d.lock()
	defer d.mu.Unlock()
	b, err := d.buildLocked(version)
	if err != nil {
		return api.ListVersionChange{}, err
	}
	return d.changeLocked(b), nil
}
