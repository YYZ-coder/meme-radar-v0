#!/usr/bin/env bash
# Fails when the staged changes would commit secrets: .env itself, a value of
# a .env key named *PASSWORD*, *SECRET*, *TOKEN* or *KEY*, the password inside
# a *_URL value, or a private-key / AWS-key pattern. Prints key names only,
# never values. Used by scripts/commit.sh and .githooks/pre-commit; ENV_FILE
# overrides .env. Changes nothing.
set -euo pipefail
cd "$(git rev-parse --show-toplevel)"
env_file=${ENV_FILE:-.env}
if git diff --cached --name-only | grep -Eq '(^|/)\.env$'; then
  echo "BLOCKED: .env is staged" >&2; exit 1
fi
leaks=$(python3 -I - "$env_file" <<'PY'
import re, subprocess, sys
diff = subprocess.run(["git", "diff", "--cached", "-U0"], capture_output=True, text=True).stdout
added = "\n".join(l[1:] for l in diff.splitlines() if l.startswith("+") and not l.startswith("+++"))
hits = []
try:
    for line in open(sys.argv[1]):
        m = re.match(r"\s*([A-Za-z_][A-Za-z0-9_]*)=(.*)", line)
        if not m:
            continue
        key, val = m.group(1), m.group(2).strip().strip("'\"")
        vals = []
        if re.search(r"PASSWORD|SECRET|TOKEN|KEY", key):
            vals.append(val)
        if key.endswith("_URL"):
            p = re.match(r"[a-z]+://[^:/@]+:([^@]+)@", val)
            if p:
                vals.append(p.group(1))
        if any(len(v) >= 6 and v in added for v in vals):
            hits.append(key)
except FileNotFoundError:
    pass
for name, pat in [("private key", r"-----BEGIN [A-Z ]*PRIVATE KEY-----"), ("AWS key", r"AKIA[0-9A-Z]{16}")]:
    if re.search(pat, added):
        hits.append(name)
print(" ".join(hits))
PY
)
if [ -n "$leaks" ]; then
  echo "BLOCKED: staged changes contain secrets: $leaks" >&2; exit 1
fi
