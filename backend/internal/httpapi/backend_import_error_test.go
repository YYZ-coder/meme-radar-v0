package httpapi_test

import (
	"bytes"
	"context"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/meme-radar/radar/internal/api"
	"github.com/meme-radar/radar/internal/contracttest"
	"github.com/meme-radar/radar/internal/httpapi"
	"github.com/meme-radar/radar/internal/mockdata"
)

type importErrorBackend struct{ *mockdata.Data }

func (importErrorBackend) CreateImport(context.Context, string, []byte) (api.ImportReport, error) {
	return api.ImportReport{}, context.DeadlineExceeded
}

// Every Backend operation can now time out, including multipart imports. The
// contract must declare the 503 response observed through that public route.
func TestBackendImportTimeoutMatchesContract(t *testing.T) {
	var upload bytes.Buffer
	form := multipart.NewWriter(&upload)
	file, err := form.CreateFormFile("file", "members.csv")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.Write([]byte("chain,address\nbsc,0x1111111111111111111111111111111111111111\n")); err != nil {
		t.Fatal(err)
	}
	if err := form.Close(); err != nil {
		t.Fatal(err)
	}
	const route = "/api/v1/admin/smart-money/imports"
	r := httptest.NewRequest(http.MethodPost, route, &upload)
	r.Header.Set("Content-Type", form.FormDataContentType())
	r.Header.Set("Authorization", "Bearer test-admin")
	rec := httptest.NewRecorder()
	httpapi.New(importErrorBackend{}, httpapi.Options{AdminToken: "test-admin"}).ServeHTTP(rec, r)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503; body %s", rec.Code, rec.Body.String())
	}
	contracttest.Check(t, http.MethodPost, route, rec)
	var failure api.ErrorBody
	if err := json.Unmarshal(rec.Body.Bytes(), &failure); err != nil {
		t.Fatal(err)
	}
	if failure.Error.Code != "busy" || rec.Header().Get("Retry-After") != "2" {
		t.Errorf("import timeout = %+v, Retry-After %q", failure, rec.Header().Get("Retry-After"))
	}
}
