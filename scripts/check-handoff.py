#!/usr/bin/env python3
"""Checks docs/handoff: prompts against task books, and every file, make
target, decision and code identifier the documents cite against the repo.
Run from anywhere: python3 scripts/check-handoff.py. Exit 1 on problems."""
import glob, os, re, subprocess, sys

ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
os.chdir(ROOT)
H = "docs/handoff"
problems, notes = [], []

def read(p):
    return open(p, encoding="utf-8").read()

books = {os.path.basename(p): read(p) for p in glob.glob(f"{H}/h*.md")}
common, prompts = read(f"{H}/00-common.md"), read(f"{H}/prompts.md")

# 1. Migration numbers: prompt == task book header == 00-common §6, unique, above the applied ones.
# Local migration files include work that has not been deployed. Use the
# explicitly recorded cloud watermark; never infer deployment from file presence.
watermark = re.search(r"已在云库执行过的迁移文件不能再改[^\n]*?当前最大\s+`(\d{4})`", common)
if watermark is None:
    problems.append("00-common.md: deployed migration watermark missing")
applied = int(watermark.group(1)) if watermark else None
assign = dict(re.findall(r"(H\d) 用 `(\d{4})`|(H\d) `(\d{4})`", common.split("## 6.")[1].split("## 7.")[0]) and
              [(a or c, b or d) for a, b, c, d in re.findall(r"(H\d) 用 `(\d{4})`|(H\d) `(\d{4})`", common.split("## 6.")[1].split("## 7.")[0])])
sections = re.split(r"^## (H\d) ", prompts, flags=re.M)[1:]
seen = {}
for mod, body in zip(sections[0::2], sections[1::2]):
    book = re.search(r"docs/handoff/(h\d-[\w-]+\.md)", body).group(1)
    if book not in books:
        problems.append(f"{mod}: task book {book} missing"); continue
    text = books[book]
    pm = re.search(r"迁移号 (\d{4})", body).group(1)
    bm = re.search(r"迁移号 `(\d{4})`", text)
    if not bm or bm.group(1) != pm: problems.append(f"{mod}: prompt migration {pm} != task book {bm and bm.group(1)}")
    if assign.get(mod) != pm: problems.append(f"{mod}: prompt migration {pm} != 00-common §6 {assign.get(mod)}")
    existing = glob.glob(f"backend/migrations/{pm}_*.sql")
    subjects = ""
    if existing:
        # Include an explicitly pending dependency merge without making a commit.
        heads = ["HEAD"]
        pending = subprocess.run(["git", "rev-parse", "-q", "--verify", "MERGE_HEAD"], capture_output=True, text=True)
        if pending.returncode == 0:
            heads.append(pending.stdout.strip())
        subjects = subprocess.run(["git", "log", "--format=%s", *heads, "--", existing[0]], capture_output=True, text=True).stdout
    owned = bool(re.search(rf"\b{mod}\b", subjects, re.IGNORECASE))
    if subjects and not owned:
        problems.append(f"{mod}: migration {pm} is taken by another change ({existing[0]})")
    elif owned:
        notes.append(f"{mod}: merged (migration {os.path.basename(existing[0])})")
    elif applied is not None and int(pm) <= applied:
        problems.append(f"{mod}: migration {pm} not above applied {applied:04d}")
    if pm in seen: problems.append(f"{mod}: migration {pm} also used by {seen[pm]}")
    seen[pm] = mod
    # slice ids cited in the prompt exist in the task book (ranges expanded)
    for a, b in re.findall(rf"({mod}-\d+)(?:～({mod}-\d+))?", body):
        lo = int(a.split("-")[1]); hi = int(b.split("-")[1]) if b else lo
        for n in range(lo, hi + 1):
            if not re.search(rf"\b{mod}-{n}\b", text): problems.append(f"{mod}: slice {mod}-{n} cited but not in {book}")
    for doc in re.findall(r"(docs/[\w./-]+\.md)", body):
        if not os.path.exists(doc): problems.append(f"{mod}: {doc} missing")

# 1b. Each prompt sets up its own worktree, branch and unique test ports (AGENTS.md).
ports = {}
for mod, body in zip(sections[0::2], sections[1::2]):
    low, n = mod.lower(), mod[1:]
    if f"git worktree add ../meme-radar-{low} -b agent/{low}" not in body:
        problems.append(f"{mod}: worktree command for agent/{low} missing")
    m = re.search(r"RADAR_E2E_PORT=(\d+) RADAR_E2E_API_PORT=(\d+)", body)
    if not m: problems.append(f"{mod}: test ports missing"); continue
    for port in m.groups():
        if not 31000 <= int(port) <= 31999: problems.append(f"{mod}: port {port} outside 31000-31999 (playwright.config.ts)")
        if port in ports: problems.append(f"{mod}: port {port} also used by {ports[port]}")
        ports[port] = mod
    if "make ingest-up" not in body: problems.append(f"{mod}: no warning against deploying from the worktree")

# 2. Paths cited in prompts and task books: existing, or declared new by the documents.
planned = set()
for t in list(books.values()) + [prompts, common]:
    for line in t.splitlines():
        if re.search(r"新文件|新包|新增|新建|放 `?internal|代码放|SQL 放|写进|草案|新 fact|脚本", line):
            planned.update(re.findall(r"(?:internal|testdata)/[\w/.*-]+", line))
def exists(p):
    p = p.rstrip(".,）)")
    p = re.sub(r"\.[A-Z]\w*$", "", p)  # internal/pkg.Func -> internal/pkg
    cands = [p, "backend/" + p, "frontend/" + p] if not p.startswith(("backend/", "docs/", "api/", "ops/", "scripts/", "frontend/")) else [p]
    return any(glob.glob(c) or os.path.exists(c) for c in cands)
for name, t in list(books.items()) + [("prompts.md", prompts), ("00-common.md", common)]:
    for p in set(re.findall(r"(?:backend/|docs/|api/|ops/|scripts/|frontend/)?(?:internal|testdata|migrations|cmd|e2e|observability)/[\w/.*{},-]+|scripts/[\w.-]+|api/openapi\.yaml", t)):
        p = p.rstrip(".,）)")
        if "{" in p or "<" in p or "NNNN" in p: continue
        if not exists(p) and not any(p.endswith(x.rstrip("/*")) or x.rstrip("*").rstrip("/") in p for x in planned):
            problems.append(f"{name}: path {p} neither exists nor is declared new")

# 3. make targets cited.
targets = set(re.findall(r"^([\w-]+):", read("Makefile"), re.M)) | {"backend:" + t for t in re.findall(r"^([\w-]+):", read("backend/Makefile"), re.M)}
for t in set(re.findall(r"make (?:-C backend )?([a-z][\w-]+)", prompts + common + "".join(books.values()))):
    in_backend = re.search(rf"make -C backend {t}\b", prompts + common + "".join(books.values()))
    if (in_backend and "backend:" + t not in targets) or (not in_backend and t not in targets and "backend:" + t not in targets):
        problems.append(f"make target {t} not found")

# 4. Decisions cited exist.
dec = read("docs/DECISIONS.md")
alltext = prompts + common + "".join(books.values()) + read(f"{H}/README.md")
for d in set(re.findall(r"\bD(1\d\d)\b", alltext)):
    if f"| D{d} |" not in dec and not re.search(rf"D{d} 起", alltext):  # "D156 起" reserves future numbers
        problems.append(f"decision D{d} cited but not in DECISIONS.md")

# 5. Code identifiers cited as existing (Pkg.Name / func names in backticks) are found in backend/.
code = subprocess.run(["git", "grep", "-h", "-E", "^(func|type|const|var)|CREATE (TABLE|VIEW|INDEX)", "--", "backend"], capture_output=True, text=True).stdout
code += read("backend/internal/solana/solana.go") + read("backend/internal/mockdata/lists.go")
idents = set()
for t in [prompts, common] + list(books.values()):
    idents.update(re.findall(r"`((?:store|compute|solana|evm|chaininfo|contracttest|smlist|parse|prices|metrics|testdb|httpapi)\.[A-Z]\w+)", t))
    idents.update(re.findall(r"`(refreshCandles|queueCommit|tickLocked|ApplyParsedMany|ResetParsed|RefreshStats|FillValues|RefreshCoverage|EnsureWindowGap|PostBalances|FindProgramAddress)`", t))
newish = set(re.findall(r"func \(s \*Store\) (\w+)", "".join(books.values()))) | {"ChainWindows", "Balance", "Balances", "ErrRangeTooLong"}
for i in sorted(idents):
    name = i.split(".")[-1]
    if name in newish: continue
    if not re.search(rf"\b{name}\b", code): problems.append(f"identifier {i} not found in backend")

# 6. Links inside the handoff documents.
for f in glob.glob(f"{H}/*.md"):
    for m in re.finditer(r"\]\(([^)#\s]+)(#[^)]*)?\)", read(f)):
        if not m.group(1).startswith("http") and not os.path.exists(os.path.normpath(os.path.join(H, m.group(1)))):
            problems.append(f"{os.path.basename(f)}: broken link {m.group(1)}")

for n in notes: print("note:", n)
for p in problems: print("PROBLEM:", p)
print(f"checked {len(sections)//2} prompts, {len(books)} task books, {len(idents)} identifiers: {len(problems)} problem(s)")
sys.exit(1 if problems else 0)
