#!/usr/bin/env bash
# Stages everything, refuses to commit secrets, commits and reports the
# pre-commit hook (make check) result briefly.
#   scripts/commit.sh -F msg.txt        # message from a file
#   scripts/commit.sh -m "Subject" [-m "Body"]
#   scripts/commit.sh -n                # stage and scan only, no commit
# Secrets are checked by scripts/check-secrets.sh (also run by the pre-commit
# hook, so a plain git commit is checked too). ENV_FILE overrides .env.
set -euo pipefail
cd "$(dirname "$0")/.."
dry=0; args=()
while [ $# -gt 0 ]; do
  case "$1" in
    -n) dry=1; shift ;;
    -m|-F) args+=("$1" "$2"); shift 2 ;;
    *) echo "unknown argument: $1" >&2; exit 2 ;;
  esac
done
[ $dry = 1 ] || [ ${#args[@]} -gt 0 ] || { echo "usage: scripts/commit.sh (-m MSG | -F FILE | -n)" >&2; exit 2; }

git add -A
if git diff --cached --quiet; then echo "nothing to commit"; exit 0; fi
if ! scripts/check-secrets.sh; then
  git reset -q; echo "unstaged; nothing committed" >&2; exit 1
fi
echo "secrets: none found; $(git diff --cached --shortstat)"
if [ $dry = 1 ]; then echo "dry run: staged, not committed"; exit 0; fi

log=$(mktemp -t radar-commit.XXXXXX)
if git commit -q "${args[@]}" >"$log" 2>&1; then
  echo "committed $(git log --oneline -1)  (hook log: $log)"
  grep -E 'passed|failed|FAIL' "$log" | tail -3 || true
else
  echo "COMMIT FAILED (hook log: $log); last lines:"; tail -15 "$log"; exit 1
fi
[ -z "$(git status --porcelain)" ] || echo "note: working tree not clean after commit"
