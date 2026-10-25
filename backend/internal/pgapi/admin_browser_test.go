package pgapi_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/meme-radar/radar/internal/lists"
)

// TestH3PgFrontendSmoke is a manual regression runner against an isolated local
// PG database and the existing production frontend build. It does not deploy.
// Explicit builder triggers make the preparing state observable without sleeps;
// Cycle wiring, leases and concurrent workers are covered by separate PG tests.
func TestH3PgFrontendSmoke(t *testing.T) {
	if os.Getenv("RADAR_H3_PG_UI_SMOKE") != "1" {
		t.Skip("manual H3 PG UI; build frontend and set RADAR_H3_PG_UI_SMOKE=1")
	}
	f := newFixture(t)
	f.now = time.Now().UTC().Truncate(time.Millisecond)
	srv := httptest.NewServer(f.handler())
	defer srv.Close()
	worker := lists.New(f.st, nil)
	control := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.WriteHeader(405)
			return
		}
		if err := worker.Step(r.Context(), f.now); err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		w.WriteHeader(204)
	}))
	defer control.Close()
	root, err := filepath.Abs("../../..")
	if err != nil {
		t.Fatal(err)
	}
	frontend := filepath.Join(root, "frontend")
	artifacts := filepath.Join(root, ".local", "h3-pg-ui")
	if err = os.MkdirAll(artifacts, 0755); err != nil {
		t.Fatal(err)
	}
	tempRoot := filepath.Join(frontend, ".local")
	if err = os.MkdirAll(tempRoot, 0755); err != nil {
		t.Fatal(err)
	}
	temp, err := os.MkdirTemp(tempRoot, "h3-pg-playwright-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(temp)
	config := filepath.Join(temp, "playwright.config.ts")
	source := fmt.Sprintf(`import {defineConfig,devices} from '@playwright/test';
export default defineConfig({testDir:%q,workers:1,retries:0,timeout:30000,reporter:[['list'],['json',{outputFile:%q}]],outputDir:%q,
use:{baseURL:'http://127.0.0.1:31310',locale:'zh-CN',trace:'retain-on-failure'},projects:[{name:'chromium',use:{...devices['Desktop Chrome']}}],
webServer:{command:'npm run start -- --hostname 127.0.0.1 --port 31310',cwd:%q,url:'http://127.0.0.1:31310/admin/import',reuseExistingServer:false,timeout:60000,env:{RADAR_API_ORIGIN:process.env.RADAR_API_ORIGIN!,RADAR_API_MOCK:'0'}}});`, temp, filepath.Join(artifacts, "result.json"), filepath.Join(artifacts, "results"), frontend)
	if err = os.WriteFile(config, []byte(source), 0600); err != nil {
		t.Fatal(err)
	}
	probe := `import {test,expect} from '@playwright/test';
test('local PG import, activate, exclude and restore',async({page,request})=>{
 const errors:string[]=[];page.on('pageerror',e=>errors.push(e.message));
 const capture=async(name:string)=>page.screenshot({path:process.env.H3_PG_UI_ARTIFACTS+'/'+name+'.png',fullPage:true});
 const build=async()=>{const r=await request.post(process.env.H3_PG_BUILD_CONTROL!);expect(r.status()).toBe(204)};
 await page.goto('/admin/import');await page.waitForLoadState('networkidle');
 await page.getByLabel('管理令牌').fill('test-admin');await page.getByRole('button',{name:'进入'}).click();
 await expect(page.getByLabel('选择文件')).toBeVisible();
 const rows=['chain,address,label,tags,twitter,source'];
 for(let i=1;i<=4;i++)rows.push('bsc,0x'+(0x60+i).toString(16).padStart(40,'0')+',H3UI0'+i+',kol,@h3,local');
 const sol=['11111111111111111111111111111111','So11111111111111111111111111111111111111112','7LMk5tKCGXgvweApmksAvfoAVAAXQWGnJdSKTn1bGLqg','6EF8rrecthR5Dkzon8Nwu78hRvfCKubJ14M5uBEwF6P','pAMMBay6oceH9fJKBRHGP5D4bD4sWpmSwMn52FMfXEA'];
 sol.forEach((a,i)=>rows.push('solana,'+a+',H3SOL0'+i+',watch,,local'));rows.push('bsc,not-an-address,Invalid,,,local');
 await page.getByLabel('选择文件').setInputFiles({name:'h3-local-ten.csv',mimeType:'text/csv',buffer:Buffer.from(rows.join('\n')+'\n')});
 await page.getByRole('button',{name:'上传并校验'}).click();await expect(page.getByText('格式错误 1')).toBeVisible();await capture('preview');
 const apply=page.getByRole('button',{name:'确认导入 9 个地址'});
 await Promise.all([page.waitForResponse(r=>r.url().endsWith('/apply')&&r.request().method()==='POST'&&r.status()===202),apply.click()]);
 await expect(page.getByRole('status').filter({hasText:'准备'}).first()).toBeVisible();await capture('preparing');
 await page.reload();await expect(page.getByRole('status').filter({hasText:'准备'}).first()).toBeVisible();
 await build();await expect(page.locator('.inline-alert[role=status]').filter({hasText:'已生效：新增 9'})).toBeVisible();await capture('activated');
 await page.getByRole('link',{name:'名单管理'}).click();await page.getByLabel('地址或名称').fill('H3UI01');
 const member=page.getByRole('row').filter({hasText:'H3UI01'});await expect(member).toHaveCount(1);
 await member.getByRole('button',{name:'排除',exact:true}).click();await page.getByRole('dialog').getByLabel('原因').fill('local H3 check');
 await Promise.all([page.waitForResponse(r=>r.url().endsWith('/exclusions')&&r.request().method()==='POST'&&r.status()===202),page.getByRole('dialog').getByRole('button',{name:'排除'}).click()]);
 await expect(page.getByRole('dialog')).toHaveCount(0);await build();
 const excluded=page.getByRole('row').filter({hasText:'local H3 check'});await expect(excluded).toBeVisible();await capture('excluded');
 await Promise.all([page.waitForResponse(r=>r.url().includes('/exclusions/bsc/')&&r.request().method()==='DELETE'&&r.status()===202),excluded.getByRole('button',{name:'恢复'}).click()]);
 await build();await expect(excluded).toHaveCount(0);await expect(member).not.toContainText('已排除');await capture('restored');
 expect(errors).toEqual([]);
});`
	spec := filepath.Join(temp, "h3-admin.spec.ts")
	if err = os.WriteFile(spec, []byte(probe), 0600); err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(t.Context(), "npx", "playwright", "test", spec, "--config", config)
	cmd.Dir = frontend
	var env []string
	for _, v := range os.Environ() {
		if !strings.HasPrefix(v, "RADAR_API_ORIGIN=") && !strings.HasPrefix(v, "RADAR_API_MOCK=") {
			env = append(env, v)
		}
	}
	cmd.Env = append(env, "RADAR_API_MOCK=0", "RADAR_API_ORIGIN="+srv.URL, "H3_PG_UI_ARTIFACTS="+artifacts, "H3_PG_BUILD_CONTROL="+control.URL)
	out, runErr := cmd.CombinedOutput()
	if err = os.WriteFile(filepath.Join(artifacts, "browser.log"), out, 0600); err != nil {
		t.Fatal(err)
	}
	t.Logf("%s\nartifacts: %s", out, artifacts)
	if runErr != nil {
		t.Fatalf("local PG frontend failed: %v", runErr)
	}
	var active, members, excluded, applied, rows, valid, invalid int
	err = f.conn.QueryRow(context.Background(), `SELECT (SELECT version FROM sm_lists WHERE status='active'),
        (SELECT count(*) FROM sm_list_members WHERE version=4),(SELECT count(*) FROM sm_list_members WHERE version=4 AND excluded),
        (SELECT count(*) FROM sm_imports WHERE applied),(SELECT sum((totals->>'rows')::integer) FROM sm_imports),
        (SELECT sum((totals->>'valid')::integer) FROM sm_imports),(SELECT sum((totals->>'invalid')::integer) FROM sm_imports)`).Scan(&active, &members, &excluded, &applied, &rows, &valid, &invalid)
	if err != nil {
		t.Fatal(err)
	}
	if active != 4 || members != 9 || excluded != 0 || applied != 1 || rows != 10 || valid != 9 || invalid != 1 {
		t.Fatalf("UI->PG active/members/excluded/applied/rows/valid/invalid=%d/%d/%d/%d/%d/%d/%d", active, members, excluded, applied, rows, valid, invalid)
	}
}
