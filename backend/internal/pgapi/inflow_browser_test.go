package pgapi_test

import (
	"fmt"
	"net"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestPgInflowFrontendSmoke is an opt-in real-PG runner. Stock tests remain
// verbatim; fixture-specific failures are retained alongside a PG-only probe.
func TestPgInflowFrontendSmoke(t *testing.T) {
	if os.Getenv("RADAR_H4_UI_SMOKE") != "1" {
		t.Skip("build frontend and set RADAR_H4_UI_SMOKE=1")
	}
	previous := apiTime
	apiTime = time.Now().UTC().Truncate(time.Minute).Add(-time.Minute)
	defer func() { apiTime = previous }()
	f := inflowFixture(t, true)
	for _, chain := range []string{"bsc", "base", "solana"} {
		for _, w := range []string{"1m", "1h", "4h", "24h"} {
			if _, err := f.st.RebuildInflow(t.Context(), chain, w, f.now); err != nil {
				t.Fatal(err)
			}
		}
	}
	srv := httptest.NewUnstartedServer(f.handler())
	l, err := net.Listen("tcp", "127.0.0.1:31420")
	if err != nil {
		t.Fatal(err)
	}
	srv.Listener = l
	srv.Start()
	defer srv.Close()
	root, err := filepath.Abs("../../..")
	if err != nil {
		t.Fatal(err)
	}
	frontend := filepath.Join(root, "frontend")
	artifacts := filepath.Join(root, ".local", "h4-pg-ui")
	tempRoot := filepath.Join(frontend, ".local")
	if err = os.MkdirAll(tempRoot, 0755); err != nil {
		t.Fatal(err)
	}
	if err = os.MkdirAll(artifacts, 0755); err != nil {
		t.Fatal(err)
	}
	temp, err := os.MkdirTemp(tempRoot, "h4-pg-playwright-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(temp)
	probe := `import {test,expect} from '@playwright/test';
test('real PG inflow and facets use published facts on desktop and phone',async({page,request})=>{
 const errors:string[]=[];page.on('pageerror',e=>errors.push(e.message));
 const response=await request.get('/api/v1/smart-money/inflow?chain=bsc&window=5m');expect(response.status()).toBe(200);
 const data=await response.json();expect(data.total).toBe(3);expect(data.list_version).toBe('2');expect(data.items[0].sm_trades).toBe(2);expect(data.items[0].net_inflow).toBe('3');
 const facets=await(await request.get('/api/v1/smart-money/inflow/facets?chain=bsc&window=5m')).json();
 expect(facets.dexes.find((x:any)=>x.id==='pancakeswap_v2').volume_usd).toBe('8');
 await page.goto('/smart-money/inflow?chain=bsc&window=5m');await page.waitForLoadState('networkidle');
 await expect(page.locator('.token-table tbody tr[data-index]')).toHaveCount(3);
 await expect(page.locator('.token-table')).toContainText('T97');
 await page.getByRole('button',{name:'全部 DEX',exact:true}).click();
 await expect(page.getByRole('checkbox',{name:'PancakeSwap v2',exact:false})).toBeVisible();
 await page.screenshot({path:process.env.H4_PG_UI_ARTIFACTS+'/inflow-desktop.png',fullPage:true});
 await page.setViewportSize({width:390,height:844});
 expect(await page.evaluate(()=>document.documentElement.scrollWidth<=window.innerWidth)).toBe(true);
 await page.screenshot({path:process.env.H4_PG_UI_ARTIFACTS+'/inflow-phone.png',fullPage:true});
 expect(errors).toEqual([]);
});`
	if err = os.WriteFile(filepath.Join(temp, "pg-inflow.spec.ts"), []byte(probe), 0600); err != nil {
		t.Fatal(err)
	}
	for _, phase := range []struct {
		name, testDir string
		specs         []string
	}{
		{"stock", filepath.Join(frontend, "e2e"), []string{"e2e/smoke/inflow.spec.ts", "e2e/smoke/facets.spec.ts", "e2e/smoke/overview-buy-size.spec.ts"}},
		{"pg-only", temp, []string{"pg-inflow.spec.ts"}},
	} {
		t.Run(phase.name, func(t *testing.T) {
			config := filepath.Join(temp, "playwright.config.ts")
			source := fmt.Sprintf(`import {defineConfig,devices} from '@playwright/test';
export default defineConfig({testDir:%q,workers:1,retries:0,reporter:[['list'],['json',{outputFile:%q}]],outputDir:%q,
use:{baseURL:'http://127.0.0.1:31410',locale:'zh-CN',trace:'retain-on-failure'},projects:[{name:'chromium',use:{...devices['Desktop Chrome']}}],
webServer:{command:'npm run start -- --hostname 127.0.0.1 --port 31410',cwd:%q,url:'http://127.0.0.1:31410/smart-money/inflow',reuseExistingServer:false,timeout:60000,env:{RADAR_API_ORIGIN:process.env.RADAR_API_ORIGIN!}}});`, phase.testDir, filepath.Join(artifacts, phase.name+".json"), filepath.Join(artifacts, phase.name+"-results"), frontend)
			if err = os.WriteFile(config, []byte(source), 0600); err != nil {
				t.Fatal(err)
			}
			args := append([]string{"playwright", "test"}, phase.specs...)
			args = append(args, "--config", config)
			cmd := exec.CommandContext(t.Context(), "npx", args...)
			cmd.Dir = frontend
			var env []string
			for _, v := range os.Environ() {
				if !strings.HasPrefix(v, "RADAR_API_ORIGIN=") && !strings.HasPrefix(v, "RADAR_API_MOCK=") {
					env = append(env, v)
				}
			}
			cmd.Env = append(env, "RADAR_E2E_MODE=connected", "RADAR_API_MOCK=0", "RADAR_API_ORIGIN="+srv.URL, "H4_PG_UI_ARTIFACTS="+artifacts)
			out, runErr := cmd.CombinedOutput()
			if err = os.WriteFile(filepath.Join(artifacts, phase.name+".log"), out, 0600); err != nil {
				t.Fatal(err)
			}
			t.Logf("%s\nartifacts: %s", out, artifacts)
			if runErr != nil {
				t.Fatalf("Playwright %s failed: %v; original assertions retained", phase.name, runErr)
			}
		})
	}
}
