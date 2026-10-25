from pathlib import Path
import json, re, subprocess
s = Path('internal/store/inflow.go').read_text()
w = Path('internal/store/window.go').read_text()
def fn(name):
    start = s.index('func ' + name + '(')
    return s[start:s.index('\n}', start) + 2]
constants = w[w.index('const windowValuesSQL'):w.index('const mainPoolTimeSQL')]
a = s.index('var inflowColumns = ')
columns = s[a:s.index('\n\n', a)]
functions = '\n'.join(fn(n) for n in ['chainWindowsCTEs', 'chainWindowsSelect', 'inflowSelectSQL', 'longSparklineCTEs', 'inflowSparklineSQL', 'inflowBucketSQL', 'inflowPublishSelectSQL'])
code = 'package main\nimport("fmt";"strings";"os";"encoding/json")\n' + constants + '\n' + columns + '\n' + functions + '\nfunc main(){json.NewEncoder(os.Stdout).Encode(map[string]string{"long":inflowPublishSelectSQL(inflowSelectSQL(true),true),"short":inflowPublishSelectSQL(inflowSelectSQL(false),false)})}\n'
Path('/tmp/h4-workmem-export.go').write_text(code)
raw = subprocess.check_output(['go', 'run', '/tmp/h4-workmem-export.go'])
for name, q in json.loads(raw).items():
    q = q.replace('WITH windows', 'WITH bind AS (SELECT $6::bigint list_version), windows', 1)
    q = re.sub(r'smart AS \(.*?\n \), dexes', 'smart AS (SELECT NULL::bytea token_address,0::bigint trades,false unpriced_usd,false unpriced_native,NULL::numeric usd,NULL::numeric native WHERE false\n ), dexes', q, flags=re.S)
    q = q.replace('t.image_url', 'NULL::text').replace('t.socials', "'{}'::jsonb")
    q = re.sub(r"CASE WHEN h.coverage<>'unknown' THEN \(SELECT count\(\*\) FROM token_balances b JOIN sm_list_members member.*?is_holder_owner\(b.chain,b.owner\)\) END", 'NULL::bigint', q, flags=re.S)
    q = q.replace('LEFT JOIN token_holder_state h ON h.chain=t.chain AND h.token_address=t.address', "LEFT JOIN LATERAL(SELECT 'unknown'::text coverage,NULL::bigint holders)h ON true")
    q = q.replace('LEFT JOIN token_balances dev ON dev.chain=t.chain AND dev.token_address=t.address AND dev.owner=t.creator', 'LEFT JOIN LATERAL(SELECT NULL::numeric balance)dev ON true')
    Path('testdata/expect/evidence/h4-audit/workmem/' + name + '.sql').write_text(q)
print('Exported exact production SELECT/array helpers for all five windows; both settings use identical explicitly masked undeployed fields.')
