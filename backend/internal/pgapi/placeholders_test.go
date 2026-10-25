package pgapi_test

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/meme-radar/radar/internal/api"
	"github.com/meme-radar/radar/internal/contracttest"
)

type routeRequest struct {
	method, route, target, body string
	status                      int
}

func requestPG(t *testing.T, h http.Handler, c routeRequest) *httptest.ResponseRecorder {
	t.Helper()
	var body io.Reader = strings.NewReader(c.body)
	contentType := "application/json"
	if c.method == http.MethodPost && c.route == "/api/v1/admin/smart-money/imports" {
		var upload bytes.Buffer
		form := multipart.NewWriter(&upload)
		file, err := form.CreateFormFile("file", "members.csv")
		if err != nil {
			t.Fatal(err)
		}
		if _, err = file.Write([]byte("chain,address\nbsc,0x0000000000000000000000000000000000000061\n")); err != nil {
			t.Fatal(err)
		}
		if err = form.Close(); err != nil {
			t.Fatal(err)
		}
		body = &upload
		contentType = form.FormDataContentType()
	}
	req := httptest.NewRequest(c.method, c.target, body)
	req.Header.Set("Content-Type", contentType)
	req.Header.Set("Authorization", "Bearer test-admin")
	key := "11111111-1111-4111-8111-111111111111"
	if strings.Contains(c.route, "/admin/smart-money/") {
		sum := sha256.Sum256([]byte(c.method + " " + c.target))
		sum[6] = (sum[6] & 15) | 64
		sum[8] = (sum[8] & 63) | 128
		key = fmt.Sprintf("%x-%x-%x-%x-%x", sum[:4], sum[4:6], sum[6:8], sum[8:10], sum[10:16])
	}
	req.Header.Set("Idempotency-Key", key)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != c.status {
		t.Fatalf("%s %s=%d, want %d: %s", c.method, c.target, rec.Code, c.status, rec.Body.String())
	}
	contracttest.Check(t, c.method, c.route, rec)
	assertH3ContractResult(t, c, rec)
	return rec
}

func placeholderRoutes() []routeRequest {
	const token = "/api/v1/tokens/bsc/0x0000000000000000000000000000000000000061"
	const p = "/api/v1/admin/"
	return []routeRequest{
		{"GET", "/api/v1/smart-money/overview", "/api/v1/smart-money/overview?window=5m", "", 200},
		{"GET", "/api/v1/smart-money/inflow/facets", "/api/v1/smart-money/inflow/facets?window=5m", "", 503},
		{"GET", "/api/v1/smart-money/inflow", "/api/v1/smart-money/inflow?window=5m", "", 200},
		{"POST", "/api/v1/tokens/batch", "/api/v1/tokens/batch", `{"window":"5m","currency":"usd","tokens":[{"chain":"bsc","address":"0x0000000000000000000000000000000000000061"}]}`, 200},
		{"GET", "/api/v1/tokens/{chain}/{address}/holders", token + "/holders", "", 200},
		{"GET", "/api/v1/tokens/{chain}/{address}/smart-money", token + "/smart-money", "", 200},
		{"POST", p + "smart-money/imports", p + "smart-money/imports", "", 201},
		{"GET", p + "smart-money/imports/{id}", p + "smart-money/imports/im_test", "", 404},
		{"POST", p + "smart-money/imports/{id}/apply", p + "smart-money/imports/im_test/apply", "", 404},
		{"GET", p + "smart-money/list-builds/{version}", p + "smart-money/list-builds/1", "", 200},
		{"POST", p + "smart-money/list-builds/{version}/retry", p + "smart-money/list-builds/1/retry", "", 200},
		{"GET", p + "smart-money/members", p + "smart-money/members", "", 200},
		{"GET", p + "smart-money/exclusions", p + "smart-money/exclusions", "", 200},
		{"POST", p + "smart-money/exclusions", p + "smart-money/exclusions", `{"chain":"bsc","address":"0x0000000000000000000000000000000000000061","reason":"test"}`, 202},
		{"DELETE", p + "smart-money/exclusions/{chain}/{address}", p + "smart-money/exclusions/bsc/0x0000000000000000000000000000000000000061", "", 202},
		{"DELETE", p + "smart-money/members/{chain}/{address}", p + "smart-money/members/bsc/0x0000000000000000000000000000000000000061", "", 404},
	}
}

func TestPgPlaceholdersMatchContract(t *testing.T) {
	f := detailFixture(t, false)
	h := f.handler()
	for _, c := range h3ContractRoutes(t, f, placeholderRoutes()) {
		t.Run(c.method+" "+c.route, func(t *testing.T) {
			rec := requestPG(t, h, c)
			if c.status == 503 {
				var got api.ErrorBody
				if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
					t.Fatal(err)
				}
				if got.Error.Code != "api_unavailable" {
					t.Errorf("placeholder error=%+v", got)
				}
			}
			switch c.route {
			case "/api/v1/smart-money/overview":
				var got api.Overview
				if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
					t.Fatal(err)
				}
				if got.ListVersion != "1" || got.Coverage != "partial" || got.ActiveWallets != nil || len(got.InflowBars) != 16 {
					t.Errorf("initial partial overview=%+v", got)
				}
			case "/api/v1/smart-money/inflow":
				var got api.InflowPage
				if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
					t.Fatal(err)
				}
				if got.Items == nil || len(got.Items) != 0 || got.Total != 0 || got.ListVersion != "1" || got.AsOf != "2026-10-23T10:00:30.000Z" || got.NextOffset != nil || !strings.HasPrefix(got.QueryID, "q_") {
					t.Errorf("inflow=%+v", got)
				}
			case "/api/v1/tokens/batch":
				var got api.Batch
				if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
					t.Fatal(err)
				}
				if got.Items == nil || len(got.Items) != 0 || got.ListVersion != "1" {
					t.Errorf("batch=%+v", got)
				}
			case "/api/v1/tokens/{chain}/{address}/holders":
				var got api.Holders
				if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
					t.Fatal(err)
				}
				if got.Items == nil || len(got.Items) != 0 || got.Total != nil || got.NextCursor != nil || got.Coverage.Status != "unknown" || got.Coverage.Since != nil || got.Coverage.AsOf != "2026-10-23T10:00:30.000Z" {
					t.Errorf("holders=%+v", got)
				}
			case "/api/v1/tokens/{chain}/{address}/smart-money":
				var got api.TokenSmartMoney
				if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
					t.Fatal(err)
				}
				if got.Items == nil || len(got.Items) != 0 || got.Holders != nil || got.ListVersion != "1" {
					t.Errorf("smart money=%+v", got)
				}
			}
		})
	}
}
