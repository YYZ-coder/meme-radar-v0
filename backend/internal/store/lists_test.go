package store_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/meme-radar/radar/internal/api"
	"github.com/meme-radar/radar/internal/config"
	"github.com/meme-radar/radar/internal/lists"
	"github.com/meme-radar/radar/internal/metrics"
	"github.com/meme-radar/radar/internal/pgapi"
	"github.com/meme-radar/radar/internal/store"
	"github.com/meme-radar/radar/internal/testdb"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

var listTime = time.Date(2026, 10, 24, 4, 0, 0, 0, time.UTC)

const listCSV = "chain,address,label,tags,twitter\nbsc,0x0000000000000000000000000000000000000061,Alpha,kol,@alpha\nsolana,11111111111111111111111111111111,Sol,,,\n"

type listFixture struct {
	st   *store.Store
	conn *pgx.Conn
	api  *pgapi.Backend
	now  time.Time
}

func newListFixture(t *testing.T) *listFixture {
	t.Helper()
	url := testdb.New(t)
	ctx := t.Context()
	st, err := store.Open(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(st.Close)
	if err = st.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	conn, err := pgx.Connect(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close(context.Background()) })
	cfg, err := config.Load("")
	if err != nil {
		t.Fatal(err)
	}
	f := &listFixture{st: st, conn: conn, now: listTime}
	f.api = pgapi.New(st, cfg, func() time.Time { return f.now })
	return f
}
func (f *listFixture) queue(t *testing.T, csv string) string {
	t.Helper()
	report, err := f.api.CreateImport(t.Context(), "members.csv", []byte(csv))
	if err != nil {
		t.Fatal(err)
	}
	change, err := f.api.ApplyImport(t.Context(), report.ID)
	if err != nil {
		t.Fatal(err)
	}
	return change.ListVersion
}
func (f *listFixture) build(t *testing.T) {
	t.Helper()
	if err := lists.New(f.st, nil).Step(t.Context(), f.now); err != nil {
		t.Fatal(err)
	}
}

// H3-3 uses the agreed persisted version/member boundary and public build status.
// Two distinct valid addresses mean exactly two snapshot members, hand-counted.
func TestBuildActivatesMembersSnapshot(t *testing.T) {
	f := newListFixture(t)
	version := f.queue(t, listCSV)
	f.build(t)
	var oldStatus, status, build string
	var members, jobs, added int
	var activated *time.Time
	err := f.conn.QueryRow(t.Context(), `SELECT (SELECT status FROM sm_lists WHERE version=1),status,build_status,activated_at,added,
        (SELECT count(*) FROM sm_list_members WHERE version=2),(SELECT count(*) FROM jobs WHERE status='done') FROM sm_lists WHERE version=2`).Scan(&oldStatus, &status, &build, &activated, &added, &members, &jobs)
	if err != nil {
		t.Fatal(err)
	}
	if oldStatus != "retired" || status != "active" || build != "activated" || activated == nil || added != 2 || members != 2 || jobs != 1 {
		t.Fatalf("after build: old=%s new=%s/%s activated=%v added=%d members=%d jobs=%d; want retired active/activated timestamp 2/2/1", oldStatus, status, build, activated, added, members, jobs)
	}
	got, err := f.api.ListBuild(t.Context(), version)
	if err != nil {
		t.Fatal(err)
	}
	if got.ActiveVersion != "2" || got.ActivatedAt == nil || got.ChangeCounts.Added != 2 || got.ChangeCounts.TotalMembers != 2 {
		t.Fatalf("build status=%+v", got)
	}
	active, err := f.api.ActiveListVersion(t.Context())
	if err != nil || active != "2" {
		t.Fatalf("active list=%s, %v; want 2", active, err)
	}
}

// H3-4: independent literals capture the user's confirmed mock merge rule.
func TestBuildMergesExistingMembers(t *testing.T) {
	for _, tc := range []struct{ name, original, label, twitter string }{
		{"preserve existing", "Alpha,kol,@alpha", "Alpha", "@alpha"},
		{"fill missing", ",kol,", "New", "@new"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newListFixture(t)
			f.queue(t, "chain,address,label,tags,twitter\nbsc,0x0000000000000000000000000000000000000061,"+tc.original+"\n")
			f.build(t)
			report, err := f.api.CreateImport(t.Context(), "merge.csv", []byte("chain,address,label,tags,twitter\nbsc,0x0000000000000000000000000000000000000061,New,whale|kol|whale,@new\n"))
			if err != nil {
				t.Fatal(err)
			}
			if report.Totals.Existing != 1 || report.Totals.Valid != 0 || report.Rows[0].Status != "existing" {
				t.Fatalf("existing preview=%+v", report)
			}
			change, err := f.api.ApplyImport(t.Context(), report.ID)
			if err != nil {
				t.Fatal(err)
			}
			if change.Merged != 1 || change.Added != 0 || change.TotalMembers != 1 {
				t.Fatalf("merge counts=%+v", change)
			}
			f.build(t)
			var label, twitter string
			var tags, sources []string
			err = f.conn.QueryRow(t.Context(), `SELECT label,twitter,tags,sources FROM sm_list_members WHERE version=3`).Scan(&label, &twitter, &tags, &sources)
			if err != nil {
				t.Fatal(err)
			}
			if label != tc.label || twitter != tc.twitter || !reflect.DeepEqual(tags, []string{"kol", "whale"}) || !reflect.DeepEqual(sources, []string{"import"}) {
				t.Fatalf("merged member=%s/%s/%v/%v, want %s/%s/[kol whale]/[import]", label, twitter, tags, sources, tc.label, tc.twitter)
			}
			var old []string
			if err = f.conn.QueryRow(t.Context(), `SELECT tags FROM sm_list_members WHERE version=2`).Scan(&old); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(old, []string{"kol"}) {
				t.Fatalf("parent mutated: tags=%v", old)
			}
			f.queue(t, "chain,address\nbsc,0x0000000000000000000000000000000000000061\n")
			f.build(t)
			if err = f.conn.QueryRow(t.Context(), `SELECT tags FROM sm_list_members WHERE version=4`).Scan(&tags); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(tags, []string{"kol", "whale"}) {
				t.Fatalf("empty tags cleared old values: %v", tags)
			}
		})
	}
}

// H3-7: a real snapshot write failure rolls back activation. Its exclusion is
// invisible until an explicit retry builds successfully; other states are no-op.
func TestFailedBuildCanBeRetried(t *testing.T) {
	f := newListFixture(t)
	f.queue(t, listCSV)
	f.build(t)
	change, err := f.api.Exclude(t.Context(), api.BSC, "0x0000000000000000000000000000000000000061", "review")
	if err != nil {
		t.Fatal(err)
	}
	_, err = f.conn.Exec(t.Context(), `CREATE FUNCTION fail_h3_snapshot() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.version=3 THEN RAISE EXCEPTION 'injected snapshot write failure'; END IF; RETURN NEW; END $$;
        CREATE TRIGGER fail_h3_snapshot BEFORE INSERT ON sm_list_members FOR EACH ROW EXECUTE FUNCTION fail_h3_snapshot()`)
	if err != nil {
		t.Fatal(err)
	}
	failures := testutil.ToFloat64(metrics.ListBuilds.WithLabelValues("failed"))
	if err = lists.New(f.st, nil).Step(t.Context(), f.now); err == nil {
		t.Fatal("injected build unexpectedly succeeded")
	}
	got, err := f.api.ListBuild(t.Context(), change.ListVersion)
	if err != nil {
		t.Fatal(err)
	}
	if got.BuildStatus != "failed" || got.Status != "building" || got.ErrorCode == nil || *got.ErrorCode != "build_error" || got.ActiveVersion != "2" {
		t.Fatalf("failed state=%+v", got)
	}
	exclusions, err := f.api.Exclusions(t.Context(), "")
	if err != nil {
		t.Fatal(err)
	}
	if exclusions.ListVersion != "2" || len(exclusions.Items) != 0 {
		t.Fatalf("failed exclusion leaked: %+v", exclusions)
	}
	var partial int
	if err = f.conn.QueryRow(t.Context(), `SELECT count(*) FROM sm_list_members WHERE version=3`).Scan(&partial); err != nil {
		t.Fatal(err)
	}
	if partial != 0 {
		t.Fatalf("failed snapshot left %d rows", partial)
	}
	if delta := testutil.ToFloat64(metrics.ListBuilds.WithLabelValues("failed")) - failures; delta != 1 {
		t.Fatalf("failure metric increment=%v", delta)
	}
	queued, err := f.api.RetryListBuild(t.Context(), "3")
	if err != nil {
		t.Fatal(err)
	}
	if queued.BuildStatus != "queued" || queued.ErrorCode != nil || queued.ActiveVersion != "2" {
		t.Fatalf("retried state=%+v", queued)
	}
	var jobs int
	if err = f.conn.QueryRow(t.Context(), `SELECT count(*) FROM jobs WHERE payload->>'version'='3'`).Scan(&jobs); err != nil {
		t.Fatal(err)
	}
	if jobs != 2 {
		t.Fatalf("retry jobs=%d, want original failed plus one queued", jobs)
	}
	if _, err = f.conn.Exec(t.Context(), `DROP TRIGGER fail_h3_snapshot ON sm_list_members; DROP FUNCTION fail_h3_snapshot()`); err != nil {
		t.Fatal(err)
	}
	f.build(t)
	exclusions, err = f.api.Exclusions(t.Context(), "")
	if err != nil {
		t.Fatal(err)
	}
	if exclusions.ListVersion != "3" || len(exclusions.Items) != 1 || exclusions.Items[0].Reason != "review" {
		t.Fatalf("activated exclusion=%+v", exclusions)
	}
	before, err := f.api.ListBuild(t.Context(), "3")
	if err != nil {
		t.Fatal(err)
	}
	after, err := f.api.RetryListBuild(t.Context(), "3")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before, after) {
		t.Fatalf("activated retry changed state: %+v / %+v", before, after)
	}
	if err = f.conn.QueryRow(t.Context(), `SELECT count(*) FROM jobs WHERE payload->>'version'='3'`).Scan(&jobs); err != nil {
		t.Fatal(err)
	}
	if jobs != 2 {
		t.Fatalf("activated retry enqueued another job: %d", jobs)
	}
}

// H3-8: lease time is injected; no sleeps or internal worker-call assertions.
func TestJobLeaseRecoversCrashedWorker(t *testing.T) {
	f := newListFixture(t)
	f.queue(t, "chain,address\nbsc,0x0000000000000000000000000000000000000061\n")
	first, ok, err := f.st.ClaimListBuild(t.Context(), f.now)
	if err != nil || !ok {
		t.Fatalf("first claim=%+v/%t/%v", first, ok, err)
	}
	if first.Attempts != 1 || !first.LeaseUntil.Equal(listTime.Add(time.Minute)) {
		t.Fatalf("initial lease=%+v", first)
	}
	if next, ok, err := f.st.ClaimListBuild(t.Context(), f.now.Add(59*time.Second)); err != nil || ok {
		t.Fatalf("early claimant=%+v/%t/%v", next, ok, err)
	}
	second, ok, err := f.st.ClaimListBuild(t.Context(), f.now.Add(61*time.Second))
	if err != nil || !ok {
		t.Fatalf("expired lease was not recovered: %+v/%t/%v", second, ok, err)
	}
	if second.ID != first.ID || second.Version != 2 || second.Attempts != 2 {
		t.Fatalf("recovered lease=%+v, original=%+v", second, first)
	}
	address := make([]byte, 20)
	address[19] = 0x61
	members := []store.ListMember{{Chain: "bsc", Address: address, Tags: []string{}, Sources: []string{"import"}, AddedAt: listTime}}
	// The original process may resume with its old clock and old token. Its
	// attempt fence must still prevent both publication and failure marking.
	if err = f.st.ActivateListBuild(t.Context(), first, members, listTime); !errors.Is(err, store.ErrJobLeaseLost) {
		t.Fatalf("stale publication=%v", err)
	}
	if err = f.st.FailListBuild(t.Context(), first, listTime); !errors.Is(err, store.ErrJobLeaseLost) {
		t.Fatalf("stale failure marking=%v", err)
	}
	if err = f.st.ActivateListBuild(t.Context(), second, members, listTime.Add(62*time.Second)); err != nil {
		t.Fatal(err)
	}
	status, err := f.api.ListBuild(t.Context(), "2")
	if err != nil {
		t.Fatal(err)
	}
	if status.BuildStatus != "activated" || status.ActiveVersion != "2" {
		t.Fatalf("recovered activation=%+v", status)
	}
}

// H3 maintenance: retention begins at application, and compute archives rows
// hourly while keeping the report's totals and applied version queryable.
func TestAppliedImportRowsArchiveAfterSevenDays(t *testing.T) {
	f := newListFixture(t)
	report, err := f.api.CreateImport(t.Context(), "retention.csv", []byte(listCSV))
	if err != nil {
		t.Fatal(err)
	}
	f.now = f.now.Add(20 * time.Minute)
	if _, err = f.api.ApplyImport(t.Context(), report.ID); err != nil {
		t.Fatal(err)
	}
	f.build(t)
	w := lists.New(f.st, nil)
	f.now = listTime.Add(7*24*time.Hour + 19*time.Minute)
	if err = w.Step(t.Context(), f.now); err != nil {
		t.Fatal(err)
	}
	before, err := f.api.Import(t.Context(), report.ID)
	if err != nil {
		t.Fatal(err)
	}
	if before.RowsArchived || len(before.Rows) != 2 {
		t.Fatalf("archived before application+7d: %+v", before)
	}
	f.now = f.now.Add(time.Hour)
	if err = w.Step(t.Context(), f.now); err != nil {
		t.Fatal(err)
	}
	after, err := f.api.Import(t.Context(), report.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !after.RowsArchived || after.Rows == nil || len(after.Rows) != 0 || after.Totals.Rows != 2 || after.Totals.Valid != 2 || after.AppliedListVersion == nil || *after.AppliedListVersion != "2" {
		t.Fatalf("archived report=%+v", after)
	}
	var rows int
	if err = f.conn.QueryRow(t.Context(), `SELECT count(*) FROM sm_import_rows WHERE 'imp_'||replace(import_id::text,'-','')=$1`, report.ID).Scan(&rows); err != nil {
		t.Fatal(err)
	}
	if rows != 0 {
		t.Fatalf("archived rows still present: %d", rows)
	}
}

// Regression supplement: the existing API transaction and SKIP LOCKED claim
// must keep a shared import and snapshot single-use under concurrent callers.
func TestConcurrentApplyAndListBuilders(t *testing.T) {
	f := newListFixture(t)
	report, err := f.api.CreateImport(t.Context(), "race.csv", []byte(listCSV))
	if err != nil {
		t.Fatal(err)
	}
	start := make(chan struct{})
	results := make(chan error, 2)
	for i := 0; i < 2; i++ {
		go func() { <-start; _, err := f.api.ApplyImport(t.Context(), report.ID); results <- err }()
	}
	close(start)
	successes, closed := 0, 0
	for i := 0; i < 2; i++ {
		err := <-results
		if err == nil {
			successes++
			continue
		}
		var ae *api.Error
		if errors.As(err, &ae) && ae.Status == 409 && ae.Code == "import_closed" {
			closed++
		} else {
			t.Fatalf("concurrent apply=%v", err)
		}
	}
	if successes != 1 || closed != 1 {
		t.Fatalf("concurrent apply success/closed=%d/%d", successes, closed)
	}
	start = make(chan struct{})
	for i := 0; i < 2; i++ {
		go func() { <-start; results <- lists.New(f.st, nil).Step(t.Context(), listTime) }()
	}
	close(start)
	for i := 0; i < 2; i++ {
		if err := <-results; err != nil {
			t.Fatal(err)
		}
	}
	var versions, jobs, attempts, members int
	err = f.conn.QueryRow(t.Context(), `SELECT (SELECT count(*) FROM sm_lists),(SELECT count(*) FROM jobs),(SELECT sum(attempts) FROM jobs),(SELECT count(*) FROM sm_list_members WHERE version=2)`).Scan(&versions, &jobs, &attempts, &members)
	if err != nil {
		t.Fatal(err)
	}
	if versions != 2 || jobs != 1 || attempts != 1 || members != 2 {
		t.Fatalf("concurrent versions/jobs/attempts/members=%d/%d/%d/%d", versions, jobs, attempts, members)
	}
	status, err := f.api.ListBuild(t.Context(), "2")
	if err != nil || status.BuildStatus != "activated" {
		t.Fatalf("concurrent build=%+v/%v", status, err)
	}
}

// Regression supplement at the explicitly agreed schema constraint boundary.
func TestListSingleActiveConstraint(t *testing.T) {
	f := newListFixture(t)
	_, err := f.conn.Exec(t.Context(), `INSERT INTO sm_lists(version,status,build_status,created_at) VALUES(2,'active','activated',$1)`, listTime)
	var pgerr *pgconn.PgError
	if !errors.As(err, &pgerr) || pgerr.Code != "23505" || pgerr.ConstraintName != "sm_lists_one_active" {
		t.Fatalf("second active constraint=%v", err)
	}
}

// Regression supplement: both previews see the old active list, while counts
// and merge behavior must follow the latest accepted draft instead.
func TestQueuedImportsMergeAgainstAcceptedDraft(t *testing.T) {
	f := newListFixture(t)
	a, err := f.api.CreateImport(t.Context(), "a.csv", []byte("chain,address,label,tags\nbsc,0x0000000000000000000000000000000000000061,First,kol\n"))
	if err != nil {
		t.Fatal(err)
	}
	b, err := f.api.CreateImport(t.Context(), "b.csv", []byte("chain,address,label,tags\nbsc,0x0000000000000000000000000000000000000061,Second,whale\n"))
	if err != nil {
		t.Fatal(err)
	}
	if a.Totals.Valid != 1 || b.Totals.Valid != 1 {
		t.Fatalf("unapplied previews should both be valid: %+v/%+v", a.Totals, b.Totals)
	}
	first, err := f.api.ApplyImport(t.Context(), a.ID)
	if err != nil {
		t.Fatal(err)
	}
	second, err := f.api.ApplyImport(t.Context(), b.ID)
	if err != nil {
		t.Fatal(err)
	}
	if first.Added != 1 || first.TotalMembers != 1 || second.Merged != 1 || second.Added != 0 || second.TotalMembers != 1 {
		t.Fatalf("accepted draft counts=%+v/%+v", first, second)
	}
	f.build(t)
	f.build(t)
	members, err := f.api.Members(t.Context(), "", "all", "", "", "", 100)
	if err != nil {
		t.Fatal(err)
	}
	if members.ListVersion != "3" || members.Total != 1 || len(members.Items) != 1 || members.Items[0].Label == nil || *members.Items[0].Label != "First" || !reflect.DeepEqual(members.Items[0].Tags, []string{"kol", "whale"}) {
		t.Fatalf("queued imports=%+v", members)
	}
}

// Regression supplement: exclusion intent may predate a wallet's membership.
// A queued future restore cannot contaminate the earlier import's snapshot.
func TestExcludedNonmemberImportUsesVersionedIntention(t *testing.T) {
	f := newListFixture(t)
	const address = "0x0000000000000000000000000000000000000061"
	if _, err := f.api.Exclude(t.Context(), api.BSC, address, "before membership"); err != nil {
		t.Fatal(err)
	}
	f.queue(t, "chain,address\nbsc,"+address+"\n")
	if _, err := f.api.Restore(t.Context(), api.BSC, address); err != nil {
		t.Fatal(err)
	}
	f.build(t)
	f.build(t)
	members, err := f.api.Members(t.Context(), "", "all", "", "", "", 100)
	if err != nil {
		t.Fatal(err)
	}
	if members.ListVersion != "3" || len(members.Items) != 1 || !members.Items[0].Excluded {
		t.Fatalf("future restore contaminated version 3: %+v", members)
	}
	f.build(t)
	members, err = f.api.Members(t.Context(), "", "all", "", "", "", 100)
	if err != nil {
		t.Fatal(err)
	}
	if members.ListVersion != "4" || len(members.Items) != 1 || members.Items[0].Excluded {
		t.Fatalf("version 4 restore=%+v", members)
	}
}

// H3 logging is an agreed operational output; both records must contain the
// version, member count and elapsed duration required by the task book.
func TestListBuildLogsVersionMembersAndDuration(t *testing.T) {
	f := newListFixture(t)
	f.queue(t, "chain,address\nbsc,0x0000000000000000000000000000000000000061\n")
	var output bytes.Buffer
	log := slog.New(slog.NewJSONHandler(&output, nil))
	if err := lists.New(f.st, log).Step(t.Context(), listTime); err != nil {
		t.Fatal(err)
	}
	dec := json.NewDecoder(&output)
	found := map[string]bool{}
	for dec.More() {
		var record map[string]any
		if err := dec.Decode(&record); err != nil {
			t.Fatal(err)
		}
		msg, ok := record["msg"].(string)
		if !ok {
			t.Fatalf("log message missing: %v", record)
		}
		if record["role"] != "compute" || record["chain"] != "all" || record["version"] != float64(2) || record["members"] != float64(1) || record["took"] == nil {
			t.Fatalf("incomplete %s log: %v", msg, record)
		}
		found[msg] = true
	}
	if !found["list build started"] || !found["list build activated"] {
		t.Fatalf("missing start/finish logs: %v", found)
	}
}

// H3 schema review: 02-data-model specifies UUID import keys. Public reports
// keep mock-compatible opaque imp_ strings; the persistent key remains UUID.
func TestImportIdentifiersUseUUIDStorage(t *testing.T) {
	f := newListFixture(t)
	report, err := f.api.CreateImport(t.Context(), "uuid.csv", []byte(listCSV))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(report.ID, "imp_") || len(report.ID) != 36 {
		t.Fatalf("public import ID=%q", report.ID)
	}
	var typ string
	if err = f.conn.QueryRow(t.Context(), `SELECT pg_typeof(id)::text FROM sm_imports`).Scan(&typ); err != nil {
		t.Fatal(err)
	}
	if typ != "uuid" {
		t.Fatalf("persisted import ID type=%s, want uuid per 02-data-model section 6", typ)
	}
	got, err := f.api.Import(t.Context(), report.ID)
	if err != nil || got.ID != report.ID {
		t.Fatalf("opaque ID round trip=%+v/%v", got, err)
	}
	if _, err = f.api.Import(t.Context(), "imp_missing"); err == nil {
		t.Fatal("unknown import should be not_found")
	} else {
		var ae *api.Error
		if !errors.As(err, &ae) || ae.Status != 404 || ae.Code != "not_found" {
			t.Fatalf("unknown ID=%v", err)
		}
	}
}

// A repeated exclusion changes reason, not the record's creation time. Version
// order remains authoritative even if the injected API clock moves backwards.
func TestRepeatedExclusionPreservesCreationTime(t *testing.T) {
	f := newListFixture(t)
	const address = "0x0000000000000000000000000000000000000061"
	if _, err := f.api.Exclude(t.Context(), api.BSC, address, "original"); err != nil {
		t.Fatal(err)
	}
	f.build(t)
	f.now = listTime.Add(-time.Minute)
	if _, err := f.api.Exclude(t.Context(), api.BSC, address, "updated"); err != nil {
		t.Fatal(err)
	}
	f.build(t)
	got, err := f.api.Exclusions(t.Context(), "")
	if err != nil {
		t.Fatal(err)
	}
	if got.ListVersion != "3" || len(got.Items) != 1 || got.Items[0].Reason != "updated" || got.Items[0].CreatedAt != listTime.Format("2006-01-02T15:04:05.000Z") {
		t.Fatalf("repeated exclusion must retain original creation time: %+v", got)
	}
}
