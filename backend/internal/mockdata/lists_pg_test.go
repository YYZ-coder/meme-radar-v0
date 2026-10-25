package mockdata

import (
	"context"
	"reflect"
	"sort"
	"strconv"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/meme-radar/radar/internal/api"
	"github.com/meme-radar/radar/internal/config"
	"github.com/meme-radar/radar/internal/lists"
	"github.com/meme-radar/radar/internal/pgapi"
	"github.com/meme-radar/radar/internal/store"
	"github.com/meme-radar/radar/internal/testdb"
)

// TestListLifecycleMatchesMock compares public Backend responses. The fixture
// matches mock's initial version 42 and 36 seeded wallets at the agreed database
// boundary; production migrations still start at empty version 1. Only member
// order is normalized because H3 explicitly requires (chain,address) keysets.
func TestListLifecycleMatchesMock(t *testing.T) {
	ctx := t.Context()
	now := time.Date(2026, 10, 24, 7, 0, 0, 0, time.UTC)
	md := New(func() time.Time { return now })
	url := testdb.New(t)
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
	initial, err := md.Members(ctx, "", "all", "", "", "", 100)
	if err != nil {
		t.Fatal(err)
	}
	if initial.ListVersion != "42" || initial.Total != 36 {
		t.Fatalf("unexpected reference fixture=%+v", initial)
	}
	if _, err = conn.Exec(ctx, `UPDATE sm_lists SET version=42,total_members=36`); err != nil {
		t.Fatal(err)
	}
	var batch pgx.Batch
	for _, m := range initial.Items {
		at, err := time.Parse(time.RFC3339Nano, m.AddedAt)
		if err != nil {
			t.Fatal(err)
		}
		batch.Queue(`INSERT INTO sm_list_members(version,chain,address,label,tags,twitter,sources,added_at,excluded) VALUES(42,$1,from_addr($1,$2),$3,$4,$5,$6,$7,$8)`, string(m.Chain), m.Address, m.Label, m.Tags, m.Twitter, m.Sources, at, m.Excluded)
		batch.Queue(`INSERT INTO sm_wallets(chain,address,label,tags,twitter,sources,first_added_at,updated_at) VALUES($1,from_addr($1,$2),$3,$4,$5,$6,$7,$7)`, string(m.Chain), m.Address, m.Label, m.Tags, m.Twitter, m.Sources, at)
	}
	if err = conn.SendBatch(ctx, &batch).Close(); err != nil {
		t.Fatal(err)
	}
	pg := pgapi.New(st, config.Config{}, func() time.Time { return now })
	worker := lists.New(st, nil)
	compare := func(stage, version string) {
		t.Helper()
		a, err := pg.ListBuild(ctx, version)
		if err != nil {
			t.Fatal(err)
		}
		b, err := md.ListBuild(ctx, version)
		if err != nil {
			t.Fatal(err)
		}
		a.CreatedAt, b.CreatedAt = "", ""
		if a.ActivatedAt != nil {
			*a.ActivatedAt = ""
		}
		if b.ActivatedAt != nil {
			*b.ActivatedAt = ""
		}
		if !reflect.DeepEqual(a, b) {
			t.Fatalf("%s build PG=%+v mock=%+v", stage, a, b)
		}
		ac, err := pg.ListChange(ctx, version)
		if err != nil {
			t.Fatal(err)
		}
		bc, err := md.ListChange(ctx, version)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(ac, bc) {
			t.Fatalf("%s change PG=%+v mock=%+v", stage, ac, bc)
		}
		am, err := pg.Members(ctx, "", "all", "", "", "", 100)
		if err != nil {
			t.Fatal(err)
		}
		bm, err := md.Members(ctx, "", "all", "", "", "", 100)
		if err != nil {
			t.Fatal(err)
		}
		normalize := func(m *api.Members) {
			for i := range m.Items {
				m.Items[i].AddedAt = ""
			}
			sort.Slice(m.Items, func(i, j int) bool {
				a, b := m.Items[i], m.Items[j]
				if a.Chain != b.Chain {
					return a.Chain < b.Chain
				}
				return a.Address < b.Address
			})
		}
		normalize(&am)
		normalize(&bm)
		if !reflect.DeepEqual(am, bm) {
			t.Fatalf("%s members disagree: PG=%+v mock=%+v", stage, am, bm)
		}
		ae, err := pg.Exclusions(ctx, "")
		if err != nil {
			t.Fatal(err)
		}
		be, err := md.Exclusions(ctx, "")
		if err != nil {
			t.Fatal(err)
		}
		normalizeExclusions := func(e *api.Exclusions) {
			for i := range e.Items {
				e.Items[i].CreatedAt = ""
			}
			sort.Slice(e.Items, func(i, j int) bool {
				a, b := e.Items[i], e.Items[j]
				if a.Chain != b.Chain {
					return a.Chain < b.Chain
				}
				return a.Address < b.Address
			})
		}
		normalizeExclusions(&ae)
		normalizeExclusions(&be)
		if !reflect.DeepEqual(ae, be) {
			t.Fatalf("%s exclusions PG=%+v mock=%+v", stage, ae, be)
		}
	}
	accept := func(stage string, a api.ListVersionChange, ae error, b api.ListVersionChange, be error) string {
		t.Helper()
		if ae != nil || be != nil {
			t.Fatalf("%s errors=%v / %v", stage, ae, be)
		}
		if !reflect.DeepEqual(a, b) {
			t.Fatalf("%s accepted PG=%+v mock=%+v", stage, a, b)
		}
		compare(stage+" queued", a.ListVersion)
		return a.ListVersion
	}
	activate := func(stage, version string) {
		t.Helper()
		now = now.Add(3 * time.Second)
		if err := worker.Step(ctx, now); err != nil {
			t.Fatal(err)
		}
		compare(stage+" activated", version)
	}
	const csv = "chain,address,label,tags,twitter\nbsc,0x0000000000000000000000000000000000000061,Alpha,kol,@alpha\nsolana,11111111111111111111111111111111,Sol,whale,\n"
	ar, err := pg.CreateImport(ctx, "same.csv", []byte(csv))
	if err != nil {
		t.Fatal(err)
	}
	br, err := md.CreateImport(ctx, "same.csv", []byte(csv))
	if err != nil {
		t.Fatal(err)
	}
	if ar.Totals != br.Totals || !reflect.DeepEqual(ar.Rows, br.Rows) {
		t.Fatalf("preview differs: %+v / %+v", ar, br)
	}
	a, ae := pg.ApplyImport(ctx, ar.ID)
	b, be := md.ApplyImport(ctx, br.ID)
	if a.Added != 2 || a.TotalMembers != 38 {
		t.Fatalf("independent import expectation=%+v, want added 2 / total 38", a)
	}
	v := accept("import", a, ae, b, be)
	activate("import", v)
	a, ae = pg.RemoveMember(ctx, api.BSC, "0x0000000000000000000000000000000000000061")
	b, be = md.RemoveMember(ctx, api.BSC, "0x0000000000000000000000000000000000000061")
	if a.Removed != 1 || a.TotalMembers != 37 {
		t.Fatalf("independent removal expectation=%+v", a)
	}
	v = accept("remove", a, ae, b, be)
	activate("remove", v)
	const sol = "11111111111111111111111111111111"
	a, ae = pg.Exclude(ctx, api.Solana, sol, "first")
	b, be = md.Exclude(ctx, api.Solana, sol, "first")
	v = accept("exclude", a, ae, b, be) // Includes exclusions after request, before build.
	activate("exclude", v)              // Includes exclusions after activation.
	a, ae = pg.Restore(ctx, api.Solana, sol)
	b, be = md.Restore(ctx, api.Solana, sol)
	v = accept("restore", a, ae, b, be)
	activate("restore", v)
	a, ae = pg.Exclude(ctx, api.Solana, sol, "after retry")
	b, be = md.Exclude(ctx, api.Solana, sol, "after retry")
	v = accept("failing exclusion", a, ae, b, be)
	if _, err = conn.Exec(ctx, `CREATE FUNCTION fail_h3_parity() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'injected snapshot failure'; END $$;
        CREATE TRIGGER fail_h3_parity BEFORE INSERT ON sm_list_members FOR EACH ROW EXECUTE FUNCTION fail_h3_parity()`); err != nil {
		t.Fatal(err)
	}
	if err = worker.Step(ctx, now); err == nil {
		t.Fatal("PG failure injection did not fail")
	}
	// User explicitly approved this sole private-field setup: the deterministic
	// mock has no public failure source. All comparisons still use public APIs.
	iv, err := strconv.Atoi(v)
	if err != nil {
		t.Fatal(err)
	}
	md.mu.Lock()
	md.builds[iv].state = "failed"
	md.builds[iv].errorCode = ptr("build_error")
	md.mu.Unlock()
	compare("failed exclusion remains invisible", v)
	ast, ae := pg.RetryListBuild(ctx, v)
	bst, be := md.RetryListBuild(ctx, v)
	if ae != nil || be != nil {
		t.Fatalf("retry errors=%v/%v", ae, be)
	}
	ast.CreatedAt, bst.CreatedAt = "", ""
	if !reflect.DeepEqual(ast, bst) {
		t.Fatalf("retry PG=%+v mock=%+v", ast, bst)
	}
	compare("retry queued", v)
	if _, err = conn.Exec(ctx, `DROP TRIGGER fail_h3_parity ON sm_list_members; DROP FUNCTION fail_h3_parity()`); err != nil {
		t.Fatal(err)
	}
	activate("retry", v)
	a, ae = pg.Exclude(ctx, api.Solana, sol, "updated reason")
	b, be = md.Exclude(ctx, api.Solana, sol, "updated reason")
	v = accept("repeat exclusion", a, ae, b, be)
	activate("repeat exclusion", v)
	aeList, err := pg.Exclusions(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	beList, err := md.Exclusions(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(aeList, beList) {
		t.Fatalf("repeat exclusion creation/reason differs: %+v / %+v", aeList, beList)
	}
	dual := initial.Items[0]
	if !reflect.DeepEqual(dual.Sources, []string{"import", "auto"}) {
		t.Fatalf("unexpected dual-source fixture: %+v", dual)
	}
	a, ae = pg.RemoveMember(ctx, dual.Chain, dual.Address)
	b, be = md.RemoveMember(ctx, dual.Chain, dual.Address)
	if a.Merged != 1 || a.Removed != 0 || a.TotalMembers != 37 {
		t.Fatalf("dual-source removal=%+v", a)
	}
	v = accept("remove imported source but retain auto", a, ae, b, be)
	activate("remove imported source but retain auto", v)
	dualCSV := "chain,address,label,tags,twitter\n" + string(dual.Chain) + "," + dual.Address + ",New,watch,@new\n"
	ar, err = pg.CreateImport(ctx, "dual.csv", []byte(dualCSV))
	if err != nil {
		t.Fatal(err)
	}
	br, err = md.CreateImport(ctx, "dual.csv", []byte(dualCSV))
	if err != nil {
		t.Fatal(err)
	}
	if ar.Totals.Existing != 1 || br.Totals.Existing != 1 {
		t.Fatalf("dual-source existing preview=%+v/%+v", ar.Totals, br.Totals)
	}
	a, ae = pg.ApplyImport(ctx, ar.ID)
	b, be = md.ApplyImport(ctx, br.ID)
	if a.Merged != 1 || a.TotalMembers != 37 {
		t.Fatalf("dual-source reimport=%+v", a)
	}
	v = accept("reimport after auto source", a, ae, b, be)
	activate("reimport after auto source", v)
	dualMembers, err := pg.Members(ctx, dual.Chain, "all", "", dual.Address, "", 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(dualMembers.Items) != 1 || !reflect.DeepEqual(dualMembers.Items[0].Sources, []string{"auto", "import"}) {
		t.Fatalf("mock source order=%+v", dualMembers)
	}
	before, err := pg.ListBuild(ctx, v)
	if err != nil {
		t.Fatal(err)
	}
	after, err := pg.RetryListBuild(ctx, v)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before, after) {
		t.Fatalf("activated retry changed status: %+v/%+v", before, after)
	}
}
