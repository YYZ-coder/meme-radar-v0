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

	"github.com/meme-radar/radar/internal/evm"
	"github.com/meme-radar/radar/internal/parse"
	"github.com/meme-radar/radar/internal/store"
)

// TestPgFrontendSmoke is an explicit local-browser runner. Ordinary Go tests
// skip it; run after a production frontend build with RADAR_PG_UI_SMOKE=1.
// The fixture and listener are always local, and every failed browser assertion
// is propagated. The stock suite's expected placeholder failures are documented.
func TestPgFrontendSmoke(t *testing.T) {
	if os.Getenv("RADAR_PG_UI_SMOKE") != "1" {
		t.Skip("manual PG browser smoke; build frontend and set RADAR_PG_UI_SMOKE=1")
	}
	previous := apiTime
	// Place facts in closed 1m/5m buckets before the browser query cutoff.
	apiTime = time.Now().UTC().Truncate(5 * time.Minute).Add(-5 * time.Minute)
	defer func() { apiTime = previous }()
	f := widestFixture(t, true)
	ctx := context.Background()
	key, err := evm.ParseAddress("0xc74408c29d7826a937b4d3ae6cc69dbd74004444")
	if err != nil {
		t.Fatal(err)
	}
	var last uint64
	if err := f.conn.QueryRow(ctx, "SELECT max(block_num) FROM chain_blocks WHERE chain='bsc'").Scan(&last); err != nil {
		t.Fatal(err)
	}
	six := int16(6)
	pool, quote := raw(20, 0xaf), raw(20, 0xc6)
	at := apiTime.Add(36 * time.Second)
	f.apply(t, "bsc", last+1, at, parse.Facts{Tokens: []parse.Token{{Address: key[:], Created: true, Symbol: "PHONE", Name: "Browser fixture", Decimals: &six, TotalSupply: "1000000000000000"}}, Pools: []parse.Pool{{ID: pool, Token: key[:], Quote: quote, QuoteSymbol: "BNB", Protocol: "pancake_v2", Dex: "pancake_v2", Created: true, ReserveToken: "1000000", ReserveQuote: "1000000000000000000"}}, Trades: []parse.Trade{{Tx: 1, Ix: 1, TxHash: raw(32, 9), Pool: pool, Token: key[:], Wallet: raw(20, 0xee), Attribution: "confirmed", Side: "buy", TokenAmount: "1000000", QuoteAmount: "1000000000000000", QuoteSymbol: "BNB", PriceNative: "0.001", ValueNative: "0.001", Decoder: "test", Version: 1}}})

	f.now = apiTime.Add(2 * time.Minute)
	f.apply(t, "bsc", last+2, f.now, parse.Facts{})
	for _, chain := range []string{"solana", "base"} {
		f.apply(t, chain, 4, f.now, parse.Facts{})
		if err := f.st.RefreshCoverage(ctx, chain); err != nil {
			t.Fatal(err)
		}
	}

	for _, step := range []func() error{
		func() error { return f.st.FillValues(ctx, "bsc", "BNB", apiTime.Add(-time.Minute)) },
		func() error {
			return f.st.SaveMainPools(ctx, "bsc", []store.MainPoolChange{{Token: key[:], Pool: pool, Reason: "liquidity"}}, at)
		},
		func() error { return f.st.SampleLiquidity(ctx, "bsc") },
		func() error { return f.st.PriceLiquidity(ctx, "bsc", "BNB", f.now) },
		func() error { return f.st.UpdateMarket(ctx, "bsc", f.now, false) },
		func() error { return f.st.RefreshStats(ctx, "bsc", f.now) },
		func() error { return f.st.RefreshCoverage(ctx, "bsc") },
	} {
		if err := step(); err != nil {
			t.Fatal(err)
		}
	}
	srv := httptest.NewServer(f.handler())
	defer srv.Close()
	root, err := filepath.Abs("../../..")
	if err != nil {
		t.Fatal(err)
	}
	frontend := filepath.Join(root, "frontend")
	artifacts := filepath.Join(root, ".local", "h2-pg-ui")
	if err := os.MkdirAll(artifacts, 0755); err != nil {
		t.Fatal(err)
	}
	tempRoot := filepath.Join(frontend, ".local")
	if err := os.MkdirAll(tempRoot, 0755); err != nil {
		t.Fatal(err)
	}
	temp, err := os.MkdirTemp(tempRoot, "h2-pg-playwright-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(temp)
	config := filepath.Join(temp, "playwright.config.ts")
	extra := filepath.Join(temp, "pg-token.spec.ts")
	// Existing project tests are run verbatim. An extra PG-only probe covers
	// the deliberately empty/unknown states the stock mock suite cannot expect.
	probe := `import {test,expect} from '@playwright/test';
const target='/token/solana/` + solFixture + `';
test('PG token renders chart and known unknown states',async({page})=>{
 const errors:string[]=[];page.on('pageerror',e=>errors.push(e.message));
 await page.goto(target);await page.waitForLoadState('networkidle');
 await expect(page.getByRole('heading',{name:/GX1J/})).toBeVisible();
 await expect(page.getByTestId('token-chart').locator('canvas').first()).toBeVisible();
 await page.screenshot({path:process.env.H2_PG_UI_ARTIFACTS+'/token-desktop.png',fullPage:true});
 await page.getByRole('tab',{name:/持有者/}).click();
 await expect(page.getByRole('tabpanel').getByRole('note')).toBeVisible();
 await page.getByRole('tab',{name:'流动性',exact:true}).click();
 await expect(page.locator('.data-table')).toContainText('主池');
 await page.setViewportSize({width:390,height:844});
 await expect(page.getByRole('heading',{name:/GX1J/})).toBeVisible();
 expect(await page.evaluate(()=>document.documentElement.scrollWidth<=window.innerWidth)).toBe(true);
 await page.screenshot({path:process.env.H2_PG_UI_ARTIFACTS+'/token-phone.png',fullPage:true});
 expect(errors).toEqual([]);
});`
	if err := os.WriteFile(extra, []byte(probe), 0600); err != nil {
		t.Fatal(err)
	}
	for _, phase := range []struct{ name, testDir, test string }{{"stock", filepath.Join(frontend, "e2e"), "e2e/smoke/token.spec.ts"}, {"pg-only", temp, "pg-token.spec.ts"}} {
		t.Run(phase.name, func(t *testing.T) {
			source := fmt.Sprintf(`import {defineConfig,devices} from '@playwright/test';
export default defineConfig({testDir:%q,workers:1,retries:0,timeout:15000,reporter:[['list'],['json',{outputFile:%q}]],outputDir:%q,
use:{baseURL:'http://127.0.0.1:31210',locale:'zh-CN',trace:'retain-on-failure'},projects:[{name:'chromium',use:{...devices['Desktop Chrome']}}],
webServer:{command:'npm run start -- --hostname 127.0.0.1 --port 31210',cwd:%q,url:'http://127.0.0.1:31210/smart-money/inflow',reuseExistingServer:false,timeout:60000,env:{RADAR_API_ORIGIN:process.env.RADAR_API_ORIGIN!}}});`, phase.testDir, filepath.Join(artifacts, phase.name+".json"), filepath.Join(artifacts, phase.name+"-results"), frontend)
			if err := os.WriteFile(config, []byte(source), 0600); err != nil {
				t.Fatal(err)
			}
			cmd := exec.Command("npx", "playwright", "test", phase.test, "--config", config)
			cmd.Dir = frontend
			var env []string
			for _, v := range os.Environ() {
				if !strings.HasPrefix(v, "RADAR_API_ORIGIN=") && !strings.HasPrefix(v, "RADAR_API_MOCK=") {
					env = append(env, v)
				}
			}
			cmd.Env = append(env, "RADAR_E2E_MODE=connected", "RADAR_API_MOCK=0", "RADAR_API_ORIGIN="+srv.URL, "H2_PG_UI_ARTIFACTS="+artifacts)
			out, err := cmd.CombinedOutput()
			if writeErr := os.WriteFile(filepath.Join(artifacts, phase.name+".log"), out, 0600); writeErr != nil {
				t.Fatal(writeErr)
			}
			t.Logf("%s\nartifacts: %s", out, artifacts)
			if err != nil {
				t.Fatalf("Playwright %s failed: %v (preserved without weakening assertions)", phase.name, err)
			}
		})
	}
}
