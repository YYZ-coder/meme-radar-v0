package pgapi_test

import (
	"context"
	"fmt"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/meme-radar/radar/internal/httpapi"
	"github.com/meme-radar/radar/internal/pgapi"
)

// TestPgAdminFrontendSmoke runs the health/retry portion of the stock admin
// smoke against a local PostgreSQL fixture. Build frontend first, then opt in
// with RADAR_H7_PG_UI_SMOKE=1. The unrelated H3 list flow is left to its module.
func TestPgAdminFrontendSmoke(t *testing.T) {
	if os.Getenv("RADAR_H7_PG_UI_SMOKE") != "1" {
		t.Skip("build frontend and set RADAR_H7_PG_UI_SMOKE=1 for local PG browser smoke")
	}
	f := healthFixture(t)
	f.failure(t, "bsc", 7, 1, "four_meme", f.now.Add(-time.Hour))
	if err := f.st.SaveParserStatus(context.Background(), "bsc", 8, []string{"four_meme", "pancake_v2"}, f.now.Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	h := httpapi.New(pgapi.New(f.st, f.cfg, func() time.Time { return f.now }), httpapi.Options{AdminToken: "mock-admin"})
	srv := httptest.NewServer(h)
	defer srv.Close()
	root, err := filepath.Abs("../../..")
	if err != nil {
		t.Fatal(err)
	}
	frontend := filepath.Join(root, "frontend")
	artifacts := filepath.Join(root, ".local", "h7-pg-ui")
	tempRoot := filepath.Join(frontend, ".local")
	for _, p := range []string{artifacts, tempRoot} {
		if err := os.MkdirAll(p, 0755); err != nil {
			t.Fatal(err)
		}
	}
	temp, err := os.MkdirTemp(tempRoot, "h7-pg-playwright-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(temp)
	stock, err := os.ReadFile(filepath.Join(frontend, "e2e", "smoke", "admin.spec.ts"))
	if err != nil {
		t.Fatal(err)
	}
	marker := "\n  await page.getByRole(\"link\", { name: \"名单导入\" }).click();"
	prefix, _, ok := strings.Cut(string(stock), marker)
	if !ok {
		t.Fatal("stock admin smoke changed: cannot identify the health/retry boundary")
	}
	// Keep the stock gate and retry assertions; add direct wire checks and a
	// screenshot. Do not stub browser requests or change frontend source.
	prefix = strings.Replace(prefix, "  // Mock failures are shared", `  const retryResponse = page.waitForResponse(r => r.request().method() === "POST" && r.url().includes("/parse-failures/"));
  // Mock failures are shared`, 1)
	prefix = strings.Replace(prefix, "admin token gate, import, exclusion and retry", "admin token gate, health and retry (real PG)", 1)
	probe := prefix + `
  expect((await retryResponse).status()).toBe(202);
  const healthResponse = await page.request.get('/api/v1/admin/health', {headers:{Authorization:'Bearer mock-admin'}});
  expect(healthResponse.status()).toBe(200);
  expect(healthResponse.headers()['x-radar-mock']).toBeUndefined();
  const health = await healthResponse.json();
  expect(health.chains.find((c: {chain:string}) => c.chain === 'bsc').backlog_blocks).toBe(10);
  expect(health.failures[0].status).toBe('queued');
  await page.waitForLoadState('networkidle');
  await page.screenshot({path:process.env.H7_PG_UI_ARTIFACTS+'/admin-health.png',fullPage:true});
});
`
	if err := os.WriteFile(filepath.Join(temp, "pg-admin.spec.ts"), []byte(probe), 0600); err != nil {
		t.Fatal(err)
	}
	config := fmt.Sprintf(`import {defineConfig,devices} from '@playwright/test';
export default defineConfig({testDir:%q,workers:1,retries:0,timeout:30000,reporter:[['list'],['json',{outputFile:%q}]],outputDir:%q,
use:{baseURL:'http://127.0.0.1:31710',locale:'zh-CN',trace:'retain-on-failure'},projects:[{name:'chromium',use:{...devices['Desktop Chrome']}}],
webServer:{command:'npm run start -- --hostname 127.0.0.1 --port 31710',cwd:%q,url:'http://127.0.0.1:31710/admin/health',reuseExistingServer:false,timeout:60000,env:{RADAR_API_ORIGIN:process.env.RADAR_API_ORIGIN!}}});`, temp, filepath.Join(artifacts, "report.json"), filepath.Join(artifacts, "results"), frontend)
	path := filepath.Join(temp, "playwright.config.ts")
	if err := os.WriteFile(path, []byte(config), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(artifacts, "pg-admin.spec.ts"), []byte(probe), 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, "npx", "playwright", "test", "--config", path)
	cmd.Dir = frontend
	for _, e := range os.Environ() {
		if !strings.HasPrefix(e, "RADAR_API_ORIGIN=") && !strings.HasPrefix(e, "RADAR_API_MOCK=") && !strings.HasPrefix(e, "NO_PROXY=") && !strings.HasPrefix(e, "no_proxy=") {
			cmd.Env = append(cmd.Env, e)
		}
	}
	cmd.Env = append(cmd.Env, "RADAR_E2E_MODE=connected", "RADAR_API_MOCK=0", "RADAR_API_ORIGIN="+srv.URL, "H7_PG_UI_ARTIFACTS="+artifacts, "NO_PROXY=127.0.0.1,localhost", "no_proxy=127.0.0.1,localhost")
	out, err := cmd.CombinedOutput()
	if writeErr := os.WriteFile(filepath.Join(artifacts, "browser.log"), out, 0600); writeErr != nil {
		t.Fatal(writeErr)
	}
	t.Logf("%s\nartifacts: %s", out, artifacts)
	if err != nil {
		t.Fatalf("PG admin smoke failed: %v", err)
	}
	var pending int
	if err := f.conn.QueryRow(context.Background(), `SELECT count(*) FROM parse_retries WHERE done_at IS NULL`).Scan(&pending); err != nil {
		t.Fatal(err)
	}
	if pending != 1 {
		t.Errorf("browser retry wrote %d pending requests; want 1", pending)
	}
}
