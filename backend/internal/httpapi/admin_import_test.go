package httpapi_test

import (
	"bytes"
	"mime/multipart"
	"net/http"
	"strings"
	"testing"
	"time"
)

const importsRoute = "/api/v1/admin/smart-money/imports"

func (h *harness) upload(name, content string, status int, out any) {
	h.t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	if name != "" {
		fw, _ := mw.CreateFormFile("file", name)
		_, _ = fw.Write([]byte(content))
	}
	_ = mw.Close()
	rec := h.do(req{method: http.MethodPost, target: importsRoute, body: &buf, header: map[string]string{"Content-Type": mw.FormDataContentType()}, admin: true})
	h.expect(http.MethodPost, importsRoute, rec, status, out)
}

type importReport struct {
	ID     string
	Totals struct{ Rows, Valid, Invalid, Duplicate, Existing int }
	Rows   []struct{ Status string }
}

const sampleCSV = "chain,address,label,tags,twitter,source\n" +
	"solana,7LMk5tKCGXgvweApmksAvfoAVAAXQWGnJdSKTn1bGLqg,Sample,kol|test,@sample,unit\n" +
	"base,0x2046d4ee2268e5583be790fde46095bdaf4eaec6,,,,unit\n" +
	"base,0x2046d4ee2268e5583be790fde46095bdaf4eaec6,,,,unit\n" +
	"bsc,not-an-address,,,,unit\n"

// product/08-admin §1: preview classifies rows; invalid rows are listed first.
func TestImportPreview(t *testing.T) {
	h := newHarness(t)
	var rep importReport
	h.upload("list.csv", sampleCSV, http.StatusCreated, &rep)
	if rep.Totals.Rows != 4 || rep.Totals.Valid != 2 || rep.Totals.Invalid != 1 || rep.Totals.Duplicate != 1 || rep.Totals.Existing != 0 {
		t.Fatalf("totals = %+v, want 4/2/1/1/0", rep.Totals)
	}
	if rep.Rows[0].Status != "invalid" {
		t.Errorf("first row status %s, want invalid first", rep.Rows[0].Status)
	}
	var again importReport
	h.get(importsRoute+"/{id}", importsRoute+"/"+rep.ID, http.StatusOK, &again, true)
	if again.ID != rep.ID {
		t.Errorf("fetched report %s, want %s", again.ID, rep.ID)
	}
	h.get(importsRoute+"/{id}", importsRoute+"/imp_missing", http.StatusNotFound, nil, true)

	h.upload("list.json", `[{"chain":"base","address":"0x2046d4ee2268e5583be790fde46095bdaf4eaec7"}]`, http.StatusCreated, &rep)
	if rep.Totals.Valid != 1 {
		t.Errorf("JSON import valid = %d, want 1", rep.Totals.Valid)
	}
	var e errorBody
	h.upload("keys.csv", "chain,address,private_key\nsolana,7LMk5tKCGXgvweApmksAvfoAVAAXQWGnJdSKTn1bGLqg,x\n", http.StatusBadRequest, &e)
	if e.Error.Code != "secret_detected" {
		t.Errorf("secret file code = %s, want secret_detected", e.Error.Code)
	}
	h.upload("", "", http.StatusBadRequest, &e)
	if e.Error.Code != "invalid_file" {
		t.Errorf("missing file code = %s, want invalid_file", e.Error.Code)
	}
	h.upload("big.csv", "chain,address\n"+strings.Repeat("x", 5<<20), http.StatusRequestEntityTooLarge, &e)
	if e.Error.Code != "file_too_large" {
		t.Errorf("big file code = %s, want file_too_large", e.Error.Code)
	}
}

// 01-api §5: apply creates the next building version once; a second apply or an
// apply after the 30-minute expiry is 409 import_closed.
func TestImportApply(t *testing.T) {
	h := newHarness(t)
	var rep importReport
	h.upload("list.csv", sampleCSV, http.StatusCreated, &rep)
	route := importsRoute + "/{id}/apply"
	apply := func(id string, status int, out any) {
		rec := h.do(req{method: http.MethodPost, target: importsRoute + "/" + id + "/apply", admin: true})
		h.expect(http.MethodPost, route, rec, status, out)
	}
	var change struct {
		ListVersion     string `json:"list_version"`
		PreviousVersion string `json:"previous_version"`
		ActiveVersion   string `json:"active_version"`
		Status          string
		BuildStatus     string `json:"build_status"`
		Added           int
	}
	apply(rep.ID, http.StatusAccepted, &change)
	if change.ListVersion != "43" || change.PreviousVersion != "42" || change.ActiveVersion != "42" || change.Status != "building" || change.BuildStatus != "queued" || change.Added != 2 {
		t.Fatalf("apply = %+v", change)
	}
	var e errorBody
	apply(rep.ID, http.StatusConflict, &e)
	if e.Error.Code != "import_closed" {
		t.Errorf("second apply code = %s", e.Error.Code)
	}
	h.upload("list.csv", sampleCSV, http.StatusCreated, &rep)
	h.clock.Advance(31 * time.Minute)
	apply(rep.ID, http.StatusConflict, nil)
	apply("imp_missing", http.StatusNotFound, nil)
}
