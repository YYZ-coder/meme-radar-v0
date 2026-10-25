package pgapi

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/meme-radar/radar/internal/api"
	"github.com/meme-radar/radar/internal/smlist"
	"github.com/meme-radar/radar/internal/store"
)

func listError(err error) error {
	if errors.Is(err, store.ErrNotFound) {
		return api.Errorf(http.StatusNotFound, "not_found", "", "list resource not found")
	}
	if errors.Is(err, store.ErrImportClosed) {
		return api.Errorf(http.StatusConflict, "import_closed", "", "import already applied or expired")
	}
	return err
}

func importReport(v store.ListImport) api.ImportReport {
	out := api.ImportReport{ID: v.ID, FileName: v.FileName, CreatedAt: v.CreatedAt.UTC().Format("2006-01-02T15:04:05.000Z"), ExpiresAt: v.ExpiresAt.UTC().Format("2006-01-02T15:04:05.000Z"), Applied: v.Applied, RowsArchived: v.RowsArchived, Totals: api.ImportTotals(v.Totals), Rows: []api.ImportRow{}}
	if v.AppliedVersion != nil {
		version := strconv.FormatInt(*v.AppliedVersion, 10)
		out.AppliedListVersion = &version
	}
	var invalid, rest []api.ImportRow
	for _, r := range v.Rows {
		row := api.ImportRow(r)
		if row.Status == "invalid" {
			invalid = append(invalid, row)
		} else {
			rest = append(rest, row)
		}
	}
	all := append(invalid, rest...)
	out.Rows = append(out.Rows, all[:min(len(all), max(500, len(invalid)))]...)
	return out
}

// CreateImport validates the upload and stores a preview without changing membership.
func (b *Backend) CreateImport(ctx context.Context, fileName string, content []byte) (api.ImportReport, error) {
	ids, err := b.st.ActiveListWalletIDs(ctx)
	if err != nil {
		return api.ImportReport{}, err
	}
	existing := make(map[string]bool, len(ids))
	for _, id := range ids {
		existing[id] = true
	}
	rows, err := smlist.Parse(fileName, content, func(chain api.Chain, address string) bool { return existing[smlist.ID(string(chain), address)] })
	if err != nil {
		return api.ImportReport{}, err
	}
	var id [16]byte
	if _, err := rand.Read(id[:]); err != nil {
		return api.ImportReport{}, err
	}
	now := b.now().UTC()
	v := store.ListImport{ID: "imp_" + hex.EncodeToString(id[:]), FileName: fileName, CreatedAt: now, ExpiresAt: now.Add(30 * time.Minute), Rows: make([]store.ImportRow, 0, len(rows))}
	for _, r := range rows {
		v.Rows = append(v.Rows, store.ImportRow(r))
		v.Totals.Rows++
		switch r.Status {
		case "valid":
			v.Totals.Valid++
		case "invalid":
			v.Totals.Invalid++
		case "duplicate":
			v.Totals.Duplicate++
		case "existing":
			v.Totals.Existing++
		}
	}
	if err := b.st.SaveListImport(ctx, v); err != nil {
		return api.ImportReport{}, err
	}
	return importReport(v), nil
}

// Import reads the durable preview; unapplied expired reports return not_found.
func (b *Backend) Import(ctx context.Context, id string) (api.ImportReport, error) {
	v, err := b.st.ListImportReport(ctx, id, b.now())
	if err != nil {
		return api.ImportReport{}, listError(err)
	}
	return importReport(v), nil
}

func versionChange(v store.ListVersion) api.ListVersionChange {
	return api.ListVersionChange{ListVersion: strconv.FormatInt(v.Version, 10), PreviousVersion: strconv.FormatInt(v.Parent, 10), ActiveVersion: strconv.FormatInt(v.Active, 10), Status: v.Status, BuildStatus: v.BuildStatus, ChangeCounts: api.ChangeCounts{Added: v.Added, Merged: v.Merged, Removed: v.Removed, TotalMembers: v.TotalMembers}}
}

// ApplyImport queues the next version once; compute builds and activates it.
func (b *Backend) ApplyImport(ctx context.Context, id string) (api.ListVersionChange, error) {
	v, err := b.st.ApplyListImport(ctx, id, b.now().UTC())
	return versionChange(v), listError(err)
}

func buildStatus(v store.ListVersion) api.ListBuildStatus {
	st := api.ListBuildStatus{ListVersion: strconv.FormatInt(v.Version, 10), PreviousVersion: strconv.FormatInt(v.Parent, 10), ActiveVersion: strconv.FormatInt(v.Active, 10), Status: v.Status, BuildStatus: v.BuildStatus, CreatedAt: v.CreatedAt.UTC().Format("2006-01-02T15:04:05.000Z"), ActivatedAt: optionalTime(v.ActivatedAt), ErrorCode: v.ErrorCode, ChangeCounts: api.ChangeCounts{Added: v.Added, Merged: v.Merged, Removed: v.Removed, TotalMembers: v.TotalMembers}}
	if v.BuildStatus != "activated" && v.Parent != v.Active {
		parent := strconv.FormatInt(v.Parent, 10)
		st.BlockingVersion = &parent
	}
	return st
}

// ListBuild reports an accepted version's asynchronous preparation state.
func (b *Backend) ListBuild(ctx context.Context, version string) (api.ListBuildStatus, error) {
	v, err := strconv.ParseInt(version, 10, 64)
	if err != nil {
		return api.ListBuildStatus{}, listError(store.ErrNotFound)
	}
	state, err := b.st.ListBuild(ctx, v)
	return buildStatus(state), listError(err)
}

// RetryListBuild explicitly queues a new attempt for a failed version.
func (b *Backend) RetryListBuild(ctx context.Context, version string) (api.ListBuildStatus, error) {
	v, err := strconv.ParseInt(version, 10, 64)
	if err != nil {
		return api.ListBuildStatus{}, listError(store.ErrNotFound)
	}
	state, err := b.st.RetryListBuild(ctx, v, b.now().UTC())
	return buildStatus(state), listError(err)
}

type memberCursor struct {
	Chain   string `json:"chain"`
	Address []byte `json:"address"`
}

// Members reads active members using literal filters and a stable keyset order.
func (b *Backend) Members(ctx context.Context, chain api.Chain, source, tag, q, cursor string, limit int) (api.Members, error) {
	f := store.ListMemberFilter{Chain: string(chain), Source: source, Tag: tag, Q: q, Limit: limit}
	if cursor != "" {
		var c memberCursor
		raw, err := base64.RawURLEncoding.DecodeString(cursor)
		if err != nil || json.Unmarshal(raw, &c) != nil || !smlist.ValidAddress(api.Chain(c.Chain), displayAddress(c.Chain, c.Address)) {
			return api.Members{}, api.Errorf(http.StatusBadRequest, "invalid_param", "cursor", "malformed cursor")
		}
		f.AfterChain, f.AfterAddress = c.Chain, c.Address
	}
	version, err := b.st.ActiveListVersion(ctx)
	if err != nil {
		return api.Members{}, err
	}
	page, err := b.st.ListMembers(ctx, version, f)
	if err != nil {
		return api.Members{}, err
	}
	out := api.Members{ListVersion: strconv.FormatInt(version, 10), Total: page.Total, Items: make([]api.Member, 0, len(page.Items))}
	for _, m := range page.Items {
		out.Items = append(out.Items, api.Member{Chain: api.Chain(m.Chain), Address: displayAddress(m.Chain, m.Address), Label: m.Label, Tags: m.Tags, Twitter: m.Twitter, Sources: m.Sources, AddedAt: m.AddedAt.UTC().Format("2006-01-02T15:04:05.000Z"), Excluded: m.Excluded})
	}
	if page.HasMore {
		last := page.Items[len(page.Items)-1]
		raw, err := json.Marshal(memberCursor{Chain: last.Chain, Address: last.Address})
		if err != nil {
			return api.Members{}, err
		}
		next := base64.RawURLEncoding.EncodeToString(raw)
		out.NextCursor = &next
	}
	return out, nil
}

// Exclusions returns exclusions effective at active, independent of pending intentions.
func (b *Backend) Exclusions(ctx context.Context, chain api.Chain) (api.Exclusions, error) {
	version, err := b.st.ActiveListVersion(ctx)
	if err != nil {
		return api.Exclusions{}, err
	}
	rows, err := b.st.ListExclusions(ctx, version)
	if err != nil {
		return api.Exclusions{}, err
	}
	out := api.Exclusions{ListVersion: strconv.FormatInt(version, 10), Items: []api.Exclusion{}}
	for _, r := range rows {
		if chain == "" || string(chain) == r.Chain {
			out.Items = append(out.Items, api.Exclusion{Chain: api.Chain(r.Chain), Address: displayAddress(r.Chain, r.Address), Reason: r.Reason, CreatedAt: r.CreatedAt.UTC().Format("2006-01-02T15:04:05.000Z")})
		}
	}
	return out, nil
}

// Exclude queues a version retaining the member but excluding it from statistics.
func (b *Backend) Exclude(ctx context.Context, chain api.Chain, address, reason string) (api.ListVersionChange, error) {
	v, err := b.st.QueueListMutation(ctx, "exclude", string(chain), address, reason, b.now().UTC())
	return versionChange(v), listError(err)
}

// Restore queues a version lifting a previously accepted exclusion.
func (b *Backend) Restore(ctx context.Context, chain api.Chain, address string) (api.ListVersionChange, error) {
	v, err := b.st.QueueListMutation(ctx, "restore", string(chain), address, "", b.now().UTC())
	return versionChange(v), listError(err)
}

// RemoveMember drops only the imported source and keeps any automatic source.
func (b *Backend) RemoveMember(ctx context.Context, chain api.Chain, address string) (api.ListVersionChange, error) {
	v, err := b.st.QueueListMutation(ctx, "remove", string(chain), address, "", b.now().UTC())
	return versionChange(v), listError(err)
}

// ListChange returns the current view of the originally accepted change.
func (b *Backend) ListChange(ctx context.Context, version string) (api.ListVersionChange, error) {
	v, err := strconv.ParseInt(version, 10, 64)
	if err != nil {
		return api.ListVersionChange{}, listError(store.ErrNotFound)
	}
	state, err := b.st.ListBuild(ctx, v)
	return versionChange(state), listError(err)
}

func (b *Backend) activeListVersion(ctx context.Context) (string, error) {
	version, err := b.st.ActiveListVersion(ctx)
	if err != nil {
		return "", err
	}
	return strconv.FormatInt(version, 10), nil
}
