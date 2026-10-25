package pgapi_test

import (
	"bytes"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/meme-radar/radar/internal/api"
	"github.com/meme-radar/radar/internal/contracttest"
	"github.com/meme-radar/radar/internal/lists"
	"github.com/meme-radar/radar/internal/mockdata"
	"github.com/meme-radar/radar/internal/pgapi"
)

const importRoute = "/api/v1/admin/smart-money/imports"

// This is the independently specified sample in httpapi/admin_import_test.go.
const h3CSV = "chain,address,label,tags,twitter,source\n" +
	"solana,7LMk5tKCGXgvweApmksAvfoAVAAXQWGnJdSKTn1bGLqg,Sample,kol|test,@sample,unit\n" +
	"base,0x2046d4ee2268e5583be790fde46095bdaf4eaec6,,,,unit\n" +
	"base,0x2046d4ee2268e5583be790fde46095bdaf4eaec6,,,,unit\n" +
	"bsc,not-an-address,,,,unit\n"

func listRequest(t *testing.T, h http.Handler, method, route, target, content, key string, status int, out any) *httptest.ResponseRecorder {
	t.Helper()
	var body bytes.Buffer
	contentType := "application/json"
	if method == "POST" && route == importRoute {
		form := multipart.NewWriter(&body)
		file, err := form.CreateFormFile("file", "list.csv")
		if err != nil {
			t.Fatal(err)
		}
		if _, err = file.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
		if err = form.Close(); err != nil {
			t.Fatal(err)
		}
		contentType = form.FormDataContentType()
	} else {
		body.WriteString(content)
	}
	req := httptest.NewRequest(method, target, &body)
	req.Header.Set("Authorization", "Bearer test-admin")
	req.Header.Set("Content-Type", contentType)
	if key != "" {
		req.Header.Set("Idempotency-Key", key)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != status {
		t.Fatalf("%s %s = %d, want %d: %s", method, target, rec.Code, status, rec.Body.String())
	}
	contracttest.Check(t, method, route, rec)
	if out != nil {
		if err := json.Unmarshal(rec.Body.Bytes(), out); err != nil {
			t.Fatal(err)
		}
	}
	return rec
}

// H3-1 tests the agreed HTTP seam and persisted import report, with literal
// counts derived from the four input rows; mock parity is an additional oracle.
func TestPgImportPreview(t *testing.T) {
	f := newFixture(t)
	h := f.handler()
	var got api.ImportReport
	listRequest(t, h, "POST", importRoute, importRoute, h3CSV, "", 201, &got)
	wantTotals := api.ImportTotals{Rows: 4, Valid: 2, Invalid: 1, Duplicate: 1}
	if got.Totals != wantTotals {
		t.Fatalf("totals = %+v, want %+v", got.Totals, wantTotals)
	}
	wantStatuses := []string{"invalid", "valid", "valid", "duplicate"}
	statuses := []string{}
	for _, r := range got.Rows {
		statuses = append(statuses, r.Status)
	}
	if !reflect.DeepEqual(statuses, wantStatuses) {
		t.Fatalf("row statuses = %v, want %v", statuses, wantStatuses)
	}
	if got.CreatedAt != f.now.Format("2006-01-02T15:04:05.000Z") || got.ExpiresAt != f.now.Add(30*time.Minute).Format("2006-01-02T15:04:05.000Z") {
		t.Fatalf("preview times = %s / %s", got.CreatedAt, got.ExpiresAt)
	}
	var again api.ImportReport
	// Reconstruct the server to prove the report survives the API process.
	listRequest(t, f.handler(), "GET", importRoute+"/{id}", importRoute+"/"+got.ID, "", "", 200, &again)
	if !reflect.DeepEqual(got, again) {
		t.Fatalf("persisted preview differs: got %+v, want %+v", again, got)
	}
	md := mockdata.New(func() time.Time { return f.now })
	expected, err := md.CreateImport(t.Context(), "list.csv", []byte(h3CSV))
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got.Rows, expected.Rows) || got.Totals != expected.Totals {
		t.Fatalf("preview disagrees with mock: %+v / %+v", got, expected)
	}
}

const oneCSV = "chain,address,label,tags,twitter\nbsc,0x0000000000000000000000000000000000000061,Alpha,kol,@alpha\n"

func uploadH3(t *testing.T, h http.Handler, content string) api.ImportReport {
	t.Helper()
	var report api.ImportReport
	listRequest(t, h, "POST", importRoute, importRoute, content, "", 201, &report)
	return report
}

func applyH3(t *testing.T, h http.Handler, id string) api.ListVersionChange {
	t.Helper()
	var change api.ListVersionChange
	rec := listRequest(t, h, "POST", importRoute+"/{id}/apply", importRoute+"/"+id+"/apply", "", "", 202, &change)
	if rec.Header().Get("Retry-After") != "2" {
		t.Fatalf("Retry-After=%q", rec.Header().Get("Retry-After"))
	}
	return change
}

// H3-2: the API queues a durable delta and job; it cannot publish a snapshot.
func TestPgApplyImportQueuesVersion(t *testing.T) {
	f := newFixture(t)
	h := f.handler()
	report := uploadH3(t, h, oneCSV)
	got := applyH3(t, h, report.ID)
	want := api.ListVersionChange{ListVersion: "2", PreviousVersion: "1", ActiveVersion: "1", Status: "building", BuildStatus: "queued", ChangeCounts: api.ChangeCounts{Added: 1, TotalMembers: 1}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("accepted change=%+v, want %+v", got, want)
	}
	var versions, changes, jobs, members int
	err := f.conn.QueryRow(t.Context(), `SELECT (SELECT count(*) FROM sm_lists WHERE version=2 AND status='building' AND build_status='queued'),
        (SELECT count(*) FROM sm_list_changes WHERE version=2 AND op='add'),
        (SELECT count(*) FROM jobs WHERE kind='list_build' AND status='queued' AND payload->>'version'='2'),
        (SELECT count(*) FROM sm_list_members)`).Scan(&versions, &changes, &jobs, &members)
	if err != nil {
		t.Fatal(err)
	}
	if versions != 1 || changes != 1 || jobs != 1 || members != 0 {
		t.Fatalf("durable version/change/job/member rows=%d/%d/%d/%d, want 1/1/1/0", versions, changes, jobs, members)
	}
	var saved api.ImportReport
	listRequest(t, h, "GET", importRoute+"/{id}", importRoute+"/"+report.ID, "", "", 200, &saved)
	if !saved.Applied || saved.AppliedListVersion == nil || *saved.AppliedListVersion != "2" {
		t.Fatalf("applied report=%+v", saved)
	}
	var failure api.ErrorBody
	listRequest(t, h, "POST", importRoute+"/{id}/apply", importRoute+"/"+report.ID+"/apply", "", "", 409, &failure)
	if failure.Error.Code != "import_closed" {
		t.Fatalf("repeat apply=%+v", failure)
	}
	expired := uploadH3(t, h, oneCSV)
	f.now = f.now.Add(31 * time.Minute)
	listRequest(t, h, "POST", importRoute+"/{id}/apply", importRoute+"/"+expired.ID+"/apply", "", "", 409, &failure)
	if failure.Error.Code != "import_closed" {
		t.Fatalf("expired apply=%+v", failure)
	}
}

// H3-5: remove, exclude and restore are versioned HTTP changes with the
// already-frozen idempotency and 202 semantics; snapshots change only at build.
func TestRemoveExcludeRestore(t *testing.T) {
	f := newFixture(t)
	h := f.handler()
	applyH3(t, h, uploadH3(t, h, oneCSV).ID)
	build := func() {
		t.Helper()
		if err := lists.New(f.st, nil).Step(t.Context(), f.now); err != nil {
			t.Fatal(err)
		}
	}
	build()
	const prefix = "/api/v1/admin/smart-money/"
	const address = "bsc/0x0000000000000000000000000000000000000061"
	const body = `{"chain":"bsc","address":"0x0000000000000000000000000000000000000061","reason":"review"}`
	for _, c := range []struct{ method, route, target, body string }{
		{"POST", prefix + "exclusions", prefix + "exclusions", body},
		{"DELETE", prefix + "exclusions/{chain}/{address}", prefix + "exclusions/" + address, ""},
		{"DELETE", prefix + "members/{chain}/{address}", prefix + "members/" + address, ""},
	} {
		listRequest(t, h, c.method, c.route, c.target, c.body, "", 400, nil)
	}
	var excluded api.ListVersionChange
	key := "00000000-0000-4000-8000-000000000001"
	rec := listRequest(t, h, "POST", prefix+"exclusions", prefix+"exclusions", body, key, 202, &excluded)
	if rec.Header().Get("Retry-After") != "2" || excluded.ListVersion != "3" || excluded.Merged != 1 || excluded.TotalMembers != 1 {
		t.Fatalf("exclude=%+v", excluded)
	}
	var activeExcluded bool
	var records int
	if err := f.conn.QueryRow(t.Context(), `SELECT (SELECT excluded FROM sm_list_members WHERE version=2),(SELECT count(*) FROM sm_exclusions)`).Scan(&activeExcluded, &records); err != nil {
		t.Fatal(err)
	}
	if activeExcluded || records != 1 {
		t.Fatalf("before activation active excluded=%t records=%d", activeExcluded, records)
	}
	var pending api.Exclusions
	listRequest(t, h, "GET", prefix+"exclusions", prefix+"exclusions", "", "", 200, &pending)
	if pending.ListVersion != "2" || len(pending.Items) != 0 {
		t.Fatalf("pending exclude leaked: %+v", pending)
	}
	var replay api.ListVersionChange
	listRequest(t, h, "POST", prefix+"exclusions", prefix+"exclusions", body, key, 202, &replay)
	if !reflect.DeepEqual(excluded, replay) {
		t.Fatalf("idempotent replay=%+v, want %+v", replay, excluded)
	}
	build()
	if err := f.conn.QueryRow(t.Context(), `SELECT excluded FROM sm_list_members WHERE version=3`).Scan(&activeExcluded); err != nil {
		t.Fatal(err)
	}
	if !activeExcluded {
		t.Fatal("exclude did not preserve the member as excluded=true")
	}
	var exclusions api.Exclusions
	listRequest(t, h, "GET", prefix+"exclusions", prefix+"exclusions", "", "", 200, &exclusions)
	if exclusions.ListVersion != "3" || len(exclusions.Items) != 1 || exclusions.Items[0].Reason != "review" {
		t.Fatalf("exclusions=%+v", exclusions)
	}
	var restored api.ListVersionChange
	listRequest(t, h, "DELETE", prefix+"exclusions/{chain}/{address}", prefix+"exclusions/"+address, "", "00000000-0000-4000-8000-000000000002", 202, &restored)
	if restored.ListVersion != "4" || restored.Merged != 1 {
		t.Fatalf("restore=%+v", restored)
	}
	listRequest(t, h, "GET", prefix+"exclusions", prefix+"exclusions", "", "", 200, &pending)
	if pending.ListVersion != "3" || len(pending.Items) != 1 {
		t.Fatalf("pending restore leaked: %+v", pending)
	}
	build()
	if err := f.conn.QueryRow(t.Context(), `SELECT (SELECT excluded FROM sm_list_members WHERE version=4),(SELECT count(*) FROM sm_exclusions)`).Scan(&activeExcluded, &records); err != nil {
		t.Fatal(err)
	}
	if activeExcluded || records != 0 {
		t.Fatalf("restored excluded=%t records=%d", activeExcluded, records)
	}
	var removed api.ListVersionChange
	listRequest(t, h, "DELETE", prefix+"members/{chain}/{address}", prefix+"members/"+address, "", "00000000-0000-4000-8000-000000000003", 202, &removed)
	if removed.ListVersion != "5" || removed.Removed != 1 || removed.TotalMembers != 0 {
		t.Fatalf("remove=%+v", removed)
	}
	build()
	var members int
	if err := f.conn.QueryRow(t.Context(), `SELECT count(*) FROM sm_list_members WHERE version=5`).Scan(&members); err != nil {
		t.Fatal(err)
	}
	if members != 0 {
		t.Fatalf("removed snapshot members=%d", members)
	}
	var failure api.ErrorBody
	listRequest(t, h, "DELETE", prefix+"members/{chain}/{address}", prefix+"members/"+address, "", "00000000-0000-4000-8000-000000000004", 404, &failure)
	if failure.Error.Code != "not_found" {
		t.Fatalf("missing member=%+v", failure)
	}
}

// H3-9: independent wallet identities define the complete active result set.
func TestPgMembersFilterAndPaginate(t *testing.T) {
	f := newFixture(t)
	h := f.handler()
	csv := "chain,address,label,tags\n" +
		"solana,11111111111111111111111111111111,Sol Scout,kol\n" +
		"base,0x0000000000000000000000000000000000000001,Beta Alpha,test\n" +
		"bsc,0x0000000000000000000000000000000000000061,Alpha,kol\n" +
		"bsc,0x0000000000000000000000000000000000000062,Whale,whale\n" +
		"bsc,0x0000000000000000000000000000000000000063,Literal%_One,kol\n"
	applyH3(t, h, uploadH3(t, h, csv).ID)
	if err := lists.New(f.st, nil).Step(t.Context(), f.now); err != nil {
		t.Fatal(err)
	}
	applyH3(t, h, uploadH3(t, h, "chain,address\nbsc,0x0000000000000000000000000000000000000064\n").ID)
	const route = "/api/v1/admin/smart-money/members"
	for _, tc := range []struct {
		query   string
		total   int64
		address string
	}{
		{"?source=import&tag=kol&chain=bsc&q=aLpHa", 1, "0x0000000000000000000000000000000000000061"},
		{"?chain=bsc&q=0X000", 3, ""},
		{"?q=%25", 1, "0x0000000000000000000000000000000000000063"},
		{"?q=_One", 1, "0x0000000000000000000000000000000000000063"},
		{"?source=auto", 0, ""},
		{"?tag=missing", 0, ""},
	} {
		var got api.Members
		listRequest(t, h, "GET", route, route+tc.query, "", "", 200, &got)
		if got.ListVersion != "2" || got.Total != tc.total || int64(len(got.Items)) != tc.total {
			t.Fatalf("filter %s=%+v", tc.query, got)
		}
		if tc.address != "" && got.Items[0].Address != tc.address {
			t.Fatalf("filter %s address=%s", tc.query, got.Items[0].Address)
		}
	}
	identities := []string{}
	target := route + "?limit=2"
	for page := 0; page < 4; page++ {
		var got api.Members
		listRequest(t, h, "GET", route, target, "", "", 200, &got)
		if got.ListVersion != "2" || got.Total != 5 || len(got.Items) > 2 {
			t.Fatalf("page=%+v", got)
		}
		for _, m := range got.Items {
			identities = append(identities, string(m.Chain)+":"+m.Address)
		}
		if got.NextCursor == nil {
			break
		}
		if page == 3 {
			t.Fatal("pagination never terminated")
		}
		target = route + "?limit=2&cursor=" + url.QueryEscape(*got.NextCursor)
	}
	want := []string{"base:0x0000000000000000000000000000000000000001", "bsc:0x0000000000000000000000000000000000000061", "bsc:0x0000000000000000000000000000000000000062", "bsc:0x0000000000000000000000000000000000000063", "solana:11111111111111111111111111111111"}
	if !reflect.DeepEqual(identities, want) {
		t.Fatalf("paged identities=%v, want %v", identities, want)
	}
	var failure api.ErrorBody
	listRequest(t, h, "GET", route, route+"?cursor=invalid!", "", "", 400, &failure)
	if failure.Error.Code != "invalid_param" || failure.Error.Field == nil || *failure.Error.Field != "cursor" {
		t.Fatalf("bad cursor=%+v", failure)
	}
}

// H3-10 uses active membership, including the exclusion flag, for search badges.
func TestPgSearchFindsListedWallets(t *testing.T) {
	f := newFixture(t)
	h := f.handler()
	applyH3(t, h, uploadH3(t, h, oneCSV).ID)
	const route = "/api/v1/search"
	const address = "0x0000000000000000000000000000000000000061"
	var before api.Search
	getPG(t, h, route, route+"?q="+address, 200, &before)
	if len(before.Wallets) != 0 {
		t.Fatalf("pending wallet leaked: %+v", before.Wallets)
	}
	if err := lists.New(f.st, nil).Step(t.Context(), f.now); err != nil {
		t.Fatal(err)
	}
	for _, query := range []string{"?q=" + address, "?chain=bsc&q=" + address, "?q=ALPHA"} {
		var got api.Search
		getPG(t, h, route, route+query, 200, &got)
		if len(got.Wallets) != 1 || got.Wallets[0].Chain != api.BSC || got.Wallets[0].Address != address || got.Wallets[0].Label == nil || *got.Wallets[0].Label != "Alpha" || !got.Wallets[0].IsSmartMoney {
			t.Fatalf("wallet search %s=%+v", query, got.Wallets)
		}
	}
	getPG(t, h, route, route+"?chain=base&q="+address, 200, &before)
	if len(before.Wallets) != 0 {
		t.Fatalf("wrong-chain wallet=%+v", before.Wallets)
	}
	b := pgapi.New(f.st, f.cfg, func() time.Time { return f.now })
	if _, err := b.Exclude(t.Context(), api.BSC, address, "review"); err != nil {
		t.Fatal(err)
	}
	getPG(t, h, route, route+"?q="+address, 200, &before)
	if len(before.Wallets) != 1 || !before.Wallets[0].IsSmartMoney {
		t.Fatalf("pending exclusion changed search=%+v", before.Wallets)
	}
	if err := lists.New(f.st, nil).Step(t.Context(), f.now); err != nil {
		t.Fatal(err)
	}
	getPG(t, h, route, route+"?q="+address, 200, &before)
	if len(before.Wallets) != 1 || before.Wallets[0].IsSmartMoney || before.Wallets[0].Label == nil || *before.Wallets[0].Label != "Alpha" {
		t.Fatalf("excluded search=%+v", before.Wallets)
	}
}

// h3ContractRoutes gives H2's route inventory real import/build identities so
// every H3 operation has a successful, schema-validated representative request.
func h3ContractRoutes(t *testing.T, f *fixture, requests []routeRequest) []routeRequest {
	t.Helper()
	b := pgapi.New(f.st, f.cfg, func() time.Time { return f.now })
	report, err := b.CreateImport(t.Context(), "contract.csv", []byte("chain,address\nbsc,0x0000000000000000000000000000000000000061\n"))
	if err != nil {
		t.Fatal(err)
	}
	const p = "/api/v1/admin/smart-money/"
	for i := range requests {
		c := &requests[i]
		switch c.route {
		case p + "imports/{id}":
			c.target = p + "imports/" + report.ID
			c.status = 200
		case p + "imports/{id}/apply":
			c.target = p + "imports/" + report.ID + "/apply"
			c.status = 202
		case p + "list-builds/{version}":
			c.target = p + "list-builds/2"
			c.status = 200
		case p + "list-builds/{version}/retry":
			c.target = p + "list-builds/2/retry"
			c.status = 202
		case p + "members/{chain}/{address}":
			c.status = 202
		}
	}
	return requests
}

func assertH3ContractResult(t *testing.T, c routeRequest, rec *httptest.ResponseRecorder) {
	t.Helper()
	const p = "/api/v1/admin/smart-money/"
	if !strings.HasPrefix(c.route, p) || c.status >= 300 {
		return
	}
	if c.status == 202 && rec.Header().Get("Retry-After") != "2" {
		t.Fatalf("%s Retry-After=%q", c.route, rec.Header().Get("Retry-After"))
	}
	switch c.route {
	case p + "imports", p + "imports/{id}":
		var v api.ImportReport
		if err := json.Unmarshal(rec.Body.Bytes(), &v); err != nil {
			t.Fatal(err)
		}
		if v.ID == "" || v.Applied || v.Totals.Rows != 1 || v.Totals.Valid != 1 || len(v.Rows) != 1 || v.Rows[0].Status != "valid" {
			t.Fatalf("%s actual report=%+v", c.route, v)
		}
	case p + "list-builds/{version}", p + "list-builds/{version}/retry":
		var v api.ListBuildStatus
		if err := json.Unmarshal(rec.Body.Bytes(), &v); err != nil {
			t.Fatal(err)
		}
		if v.ListVersion != "2" || v.PreviousVersion != "1" || v.ActiveVersion != "1" || v.BuildStatus != "queued" || v.ChangeCounts.Added != 1 || v.ChangeCounts.TotalMembers != 1 {
			t.Fatalf("actual build=%+v", v)
		}
	case p + "imports/{id}/apply", p + "exclusions", p + "exclusions/{chain}/{address}", p + "members/{chain}/{address}":
		if c.method == "GET" {
			return
		}
		var v api.ListVersionChange
		if err := json.Unmarshal(rec.Body.Bytes(), &v); err != nil {
			t.Fatal(err)
		}
		versions := map[string]string{p + "imports/{id}/apply": "2", p + "exclusions": "3", p + "exclusions/{chain}/{address}": "4", p + "members/{chain}/{address}": "5"}
		parents := map[string]string{p + "imports/{id}/apply": "1", p + "exclusions": "2", p + "exclusions/{chain}/{address}": "3", p + "members/{chain}/{address}": "4"}
		counts := api.ChangeCounts{Merged: 1, TotalMembers: 1}
		if c.route == p+"imports/{id}/apply" {
			counts = api.ChangeCounts{Added: 1, TotalMembers: 1}
		}
		if c.route == p+"members/{chain}/{address}" {
			counts = api.ChangeCounts{Removed: 1}
		}
		if v.ListVersion != versions[c.route] || v.PreviousVersion != parents[c.route] || v.ActiveVersion != "1" || v.Status != "building" || v.BuildStatus != "queued" || v.ChangeCounts != counts {
			t.Fatalf("%s actual change=%+v", c.route, v)
		}
	case p + "members":
		var v api.Members
		if err := json.Unmarshal(rec.Body.Bytes(), &v); err != nil {
			t.Fatal(err)
		}
		if v.ListVersion != "1" || v.Total != 0 || len(v.Items) != 0 {
			t.Fatalf("actual active members=%+v", v)
		}
	}
}
