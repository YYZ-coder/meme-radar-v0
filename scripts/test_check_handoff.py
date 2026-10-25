"""Migration ownership regressions at check-handoff.py's public CLI boundary."""
from pathlib import Path
import shutil
import subprocess
import sys
import tempfile
import unittest

CHECKER = Path(__file__).with_name("check-handoff.py")


class HandoffMigrationTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory(prefix="radar-handoff-")
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        for folder in ["scripts", "docs/handoff", "backend/migrations", "backend/internal/solana", "backend/internal/mockdata"]:
            (self.root / folder).mkdir(parents=True, exist_ok=True)
        shutil.copyfile(CHECKER, self.root / "scripts/check-handoff.py")
        self.write("Makefile", "ingest-up:\n")
        self.write("backend/Makefile", "test:\n")
        self.write("backend/internal/solana/solana.go", "")
        self.write("backend/internal/mockdata/lists.go", "")
        self.write("docs/DECISIONS.md", "")
        self.write("docs/handoff/README.md", "")
        self.write("backend/migrations/0010_existing.sql", "-- deployed\n")
        # A file is not proof of deployment: this is the H2 worktree state.
        self.write("backend/migrations/0012_pending.sql", "-- local, not deployed\n")
        subprocess.run(["git", "init", "-q", str(self.root)], check=True, capture_output=True)
        self.documents()

    def write(self, path, text):
        (self.root / path).write_text(text, encoding="utf-8")

    def documents(self, deployed="0010", h1="0011", h2="0012"):
        watermark = f"已在云库执行过的迁移文件不能再改（当前最大 `{deployed}`）。\n" if deployed else ""
        self.write("docs/handoff/00-common.md", watermark + f"## 6. 多 agent\nH1 用 `{h1}`，H2 `{h2}`\n## 7. 完成标准\n")
        prompts = []
        for number, migration in [(1, h1), (2, h2)]:
            self.write(f"docs/handoff/h{number}-task.md", f"# H{number}\n迁移号 `{migration}`\n")
            prompts.append(f"## H{number} 模块\n读 docs/handoff/h{number}-task.md\n迁移号 {migration}\ngit worktree add ../meme-radar-h{number} -b agent/h{number} HEAD\nRADAR_E2E_PORT=31{number}10 RADAR_E2E_API_PORT=31{number}20\n不要执行 make ingest-up\n")
        self.write("docs/handoff/prompts.md", "\n".join(prompts))

    def check(self):
        return subprocess.run([sys.executable, str(self.root / "scripts/check-handoff.py")], cwd=self.root, capture_output=True, text=True)

    def test_pending_local_migrations_do_not_advance_deployed_watermark(self):
        result = self.check()
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        self.assertIn("0 problem(s)", result.stdout)

    def test_lowercase_module_in_commit_subject_keeps_migration_ownership(self):
        # These commits belong only to this test's throwaway repository.
        subprocess.run(["git", "add", "backend/migrations/0012_pending.sql"], cwd=self.root, check=True, capture_output=True)
        subprocess.run(["git", "-c", "user.name=Handoff Test", "-c", "user.email=handoff@example.invalid", "commit", "-q", "-m", "feat(h2): add PostgreSQL API"], cwd=self.root, check=True, capture_output=True)
        result = self.check()
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        self.assertIn("H2: merged (migration 0012_pending.sql)", result.stdout)

    def test_different_module_in_commit_subject_remains_rejected(self):
        # H20 is a different module; a prefix match must not grant H2 ownership.
        subprocess.run(["git", "add", "backend/migrations/0012_pending.sql"], cwd=self.root, check=True, capture_output=True)
        subprocess.run(["git", "-c", "user.name=Handoff Test", "-c", "user.email=handoff@example.invalid", "commit", "-q", "-m", "feat(h20): add another module migration"], cwd=self.root, check=True, capture_output=True)
        result = self.check()
        self.assertEqual(result.returncode, 1, result.stdout + result.stderr)
        self.assertIn("H2: migration 0012 is taken by another change", result.stdout)

    def test_deployed_numbers_remain_unavailable(self):
        self.documents(deployed="0012")
        result = self.check()
        self.assertEqual(result.returncode, 1, result.stdout + result.stderr)
        self.assertIn("H1: migration 0011 not above applied 0012", result.stdout)
        self.assertIn("H2: migration 0012 not above applied 0012", result.stdout)

    def test_missing_deployed_watermark_is_an_error(self):
        self.documents(deployed="")
        result = self.check()
        self.assertEqual(result.returncode, 1, result.stdout + result.stderr)
        self.assertIn("deployed migration watermark", result.stdout)

    def test_duplicate_reservations_are_still_rejected(self):
        self.documents(h1="0012", h2="0012")
        result = self.check()
        self.assertEqual(result.returncode, 1, result.stdout + result.stderr)
        self.assertIn("H2: migration 0012 also used by H1", result.stdout)

    def test_prompt_and_book_must_still_agree(self):
        self.write("docs/handoff/h2-task.md", "# H2\n迁移号 `0013`\n")
        result = self.check()
        self.assertEqual(result.returncode, 1, result.stdout + result.stderr)
        self.assertIn("H2: prompt migration 0012 != task book 0013", result.stdout)


if __name__ == "__main__":
    unittest.main()
