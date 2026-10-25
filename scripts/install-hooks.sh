#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/.."
old="$(git config --local --get core.hooksPath || true)"
if [ -n "$old" ] && [ "$old" != .githooks ]; then
  echo "Existing hook path $old: integrate .githooks/pre-commit before setup." >&2
  exit 1
fi
if [ -z "$old" ] && [ -f "$(git rev-parse --git-path hooks/pre-commit)" ]; then
  echo "An existing pre-commit hook is installed. Integrate it with .githooks before setup." >&2
  exit 1
fi
git config --local core.hooksPath .githooks
