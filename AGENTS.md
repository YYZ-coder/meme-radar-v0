# 项目开发规则

适用于本仓库所有代码和所有开发 agent。后端、前端还须分别遵守 [backend/AGENTS.md](backend/AGENTS.md) 与 [frontend/AGENTS.md](frontend/AGENTS.md)；本规则不替代既有验收门禁。

## TDD（用户要求）

新增功能、缺陷修复和改变行为的改动必须遵循测试驱动开发，按一个可观察行为的小切片推进：

1. **确定行为与边界**：沿用用户已确认的需求、任务卡和冻结契约，记录本次测试的公开接口或约定持久化边界；尚未约定的边界先澄清，继续不依赖它的工作。
2. **Red**：先写一个有独立期望的测试，在未实现目标行为的代码上运行，确认它因该行为缺失或错误而失败。缺依赖、编译错误、数据库不可用不算目标行为的红灯；必要时先建立最小可编译骨架或修复测试环境，再验证目标断言失败。
3. **Green**：只实现使该测试通过的最小改动，重新执行同一个测试。不得先写完整实现，再批量补测试；若测试首次即通过，应检查测试是否有效或行为是否已经存在。
4. **审查与重构**：变绿后再审查、按需要重构，保持该测试及受影响回归通过，再执行任务要求的集成、竞态、浏览器和提交门禁。一次完成一个行为切片，再开始下一个。
5. **保存证据**：交接记录测试名、契约边界、独立期望来源、Red时的基线SHA与新增测试diff/hash、失败断言、Green结果、执行命令及脱敏日志位置；未执行就写未执行，不凭测试数量证明TDD。不要求把故意失败的提交合入主分支。

测试验证调用方可观察的行为，避免绑定私有方法、内部调用次数或照抄实现计算期望。真实协议样本及独立期望、真实PostgreSQL事务验证按 [技术方案 §13 测试策略](TECHNICAL_OVERVIEW.md#13-testing-strategy) 执行；mock通过不能替代真实链路验收，不能削弱断言或放宽未知值来变绿。

存量代码补测称为回归补充；没有事前Red证据，不得事后宣称其按TDD开发。纯文档或格式改动不新增无意义的测试，检查文档差异与链接即可。

## 提交（所有 agent 必须遵守）

- 只在用户明确要求时提交或推送。
- **提交一律用 `scripts/commit.sh`**（`-m "说明"` 或 `-F 说明文件`；`-n` 只暂存并扫描、不提交），不要直接 `git commit`。它会暂存全部改动、拒绝提交 `.env` 与口令（`.env` 中名字含 PASSWORD/SECRET/TOKEN/KEY 的值、连接串里的密码、私钥格式；命中时只报变量名并撤销暂存），然后提交并只输出提交钩子 `make check` 的结果。
- 提交钩子（`.githooks/pre-commit`）同样先执行 `scripts/check-secrets.sh` 再跑 `make check`，所以直接 `git commit` 也会被拦；新克隆的仓库先执行一次 `make setup` 安装钩子。不要用 `--no-verify` 跳过钩子。
- 被拦下时：把口令移出代码（放 `.env`，配置里只写变量名），不要改脚本或放宽规则来绕过。

## 多 agent 并行开发（git worktree，所有 agent 必须遵守）

每个 agent 在**自己的 git worktree 与分支**上工作，不在主工作区（`/Users/yuezhang/yz/projects/meme-radar-v0`）里改代码；主工作区只用于合并与部署。

1. 建工作区（`<m>` 为模块编号小写，如 `h1`；`<base>` 为用户给的基线提交或分支，默认当前主分支）：

   ```bash
   cd /Users/yuezhang/yz/projects/meme-radar-v0
   git worktree add ../meme-radar-<m> -b agent/<m> <base>
   cd ../meme-radar-<m>
   ln -s ../meme-radar-v0/.env .env          # 只读查询云库用；.env 已被 git 忽略，commit.sh 也会拦
   (cd frontend && npm ci)                    # 新工作区没有 node_modules
   export RADAR_E2E_PORT=31<n>10 RADAR_E2E_API_PORT=31<n>20   # <n> 为模块序号（h1→1 … h7→7），避免 make check 端口冲突
   ```

   提交钩子（`core.hooksPath=.githooks`）对所有 worktree 生效；本地测试库 `make -C backend db-up` 共用一个（每个测试自建临时库，互不影响）。
2. 只在自己的分支提交（仍然只用 `scripts/commit.sh`，且只在用户要求时）。需要别的模块已合入的代码时：`git merge <主分支>`，不要从其他 agent 的分支拿。
3. **不要部署**：worktree 里禁止执行 `make ingest-up`、`make obs-up`、`docker compose … up`（compose 项目名固定为 `meme-radar-app`，会把线上运行的容器换成你分支的代码）。线上核对只做只读查询；需要部署验证时汇报并等用户安排——合并进主分支后由用户指定一个 agent 在主工作区部署。
4. 完成后汇报分支名与提交号，由用户（或用户指定的 agent）在主工作区合并：`git merge --no-ff agent/<m>`，合并后跑 `make check`；冲突在合并时解决，不要改别人的分支。
5. 已经在主工作区里改了代码的 agent，先把自己的改动搬走（只列自己的文件）：

   ```bash
   cd /Users/yuezhang/yz/projects/meme-radar-v0
   git stash push -u -m <m>-wip -- <你改过和新建的文件…>
   git worktree add ../meme-radar-<m> -b agent/<m> HEAD
   cd ../meme-radar-<m> && git stash pop "$(git stash list | grep -m1 '<m>-wip' | cut -d: -f1)"
   ```

6. 用完删除：合并后 `git worktree remove ../meme-radar-<m>`、`git branch -d agent/<m>`（用户确认后）。不要动其他已有的 worktree（如 `~/.codex/worktrees/*`、`~/.config/cogen/*`）。
