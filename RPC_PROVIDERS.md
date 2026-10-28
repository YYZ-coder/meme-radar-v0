# RPC providers

[English — default](RPC_PROVIDERS.md) · [简体中文](RPC_PROVIDERS.zh-CN.md)

QuickNode is the optional primary RPC for the project's Solana, Base and BSC mainnets. The existing public endpoints remain the automatic fallback. Without a private URL, each role continues to use its public endpoint.

## Configuration

Keep complete authenticated HTTP endpoint URLs in the ignored local .env, not in tracked YAML, source, logs or browser code:

    QUICKNODE_SOLANA_RPC_URL=
    QUICKNODE_BASE_RPC_URL=
    QUICKNODE_BSC_RPC_URL=

Set those values to the HTTP URLs supplied by QuickNode. A QN account-management API key is used for provisioning, not as a replacement for a JSON-RPC URL. Application containers do not need that management credential.

The embedded chain configuration names each variable through rpc_url_env. Custom configuration files may use the same field. A set private URL must be a valid HTTP(S) URL with a host and no fragment. An empty/unset variable keeps public-only operation.

| Chain | Private primary variable | Public ingestion fallback | Public lookup/backfill fallback |
| --- | --- | --- | --- |
| Solana | QUICKNODE_SOLANA_RPC_URL | https://api.mainnet-beta.solana.com | Same |
| Base | QUICKNODE_BASE_RPC_URL | https://mainnet.base.org | Same |
| BSC | QUICKNODE_BSC_RPC_URL | https://bsc-rpc.publicnode.com | https://bsc-dataseed.bnbchain.org |

Ingestion uses ingest.rpc_url as fallback. Backfill uses backfill.rpc_url, or the public ingestion URL when omitted. Parse, compute, enrich and API native-balance lookups use lookup_rpc_url, or the public ingestion URL when omitted. Existing role and per-method rate limits remain in effect and are shared by provider attempts.

An explicit ingest --rpc-url override selects that endpoint alone for that process, preserving operator control over private forks and custom networks.

## Failover behavior

- The primary is tried first on every call. Each distinct configured fallback is tried at most once.
- Only the project's explicitly supported read methods can use fallback. Transaction submission and unknown methods are never replayed.
- Eligible provider failures: connection/timeouts, HTTP 401/403/429/5xx, malformed/unreadable JSON-RPC responses, and JSON-RPC internal error (-32603), method unavailable (-32601) or unhealthy/rate-limited provider (-32005).
- Invalid parameters, execution reverts, skipped-slot business errors, valid null results and caller cancellation retain their meaning.
- When failover is configured, each provider attempt gets at most 15 seconds, including local limiter waiting. Public-only operation keeps its existing HTTP-client timeout. A shorter caller deadline is shared across remaining attempts. The API's existing two-second deadline remains unchanged.
- All providers failing returns an error to ordinary callers. Optional holder native balances retain the existing truthful null behavior; failure never becomes a fabricated zero.
- This is ordered failover, without a persistent circuit breaker. A later call tries the primary again. Metrics report actual attempts by host; authentication paths/queries are excluded, and logs record hosts.
- HTTP error bodies are not included in errors. Transport URL wrappers and reflected authentication-sized path/query values are redacted.

The attempt budget accommodates the measured local cold-connection delay; it is not a 200 ms latency guarantee. The performance plan still requires separate cold-start, network, SQL and API acceptance.

## Verification

Use the repository's supported Node 24 runtime for frontend gates.

    make check
    RADAR_TEST_DATABASE_URL=postgres://radar:radar@127.0.0.1:55432/postgres?sslmode=disable       make -C backend test-race

The PostgreSQL URL above is for the dedicated local test server. Each integration test creates and drops its own temporary database. Never replace it with the production business database.

The test suite covers private preference, public recovery, total outage/null semantics, caller deadlines, business errors, concurrent reads, credential redaction, recorded block ingestion/backfill, and enrichment persistence. Tests that depend on PostgreSQL skip when the test variable is unset; a passing mock suite is not real-chain deployment acceptance.

Provisioning and bounded live identity/head probes are separate from application tests. A simulated primary outage plus a real public RPC response demonstrates network fallback without changing the actual QuickNode endpoint.

## Deployment

Supply the three runtime URL variables to the existing role processes when deploying the tested revision. Keep any management API credential separate. A running binary/container does not acquire new code or environment automatically.

Follow repository worktree, merge, commit and deployment rules. Worktrees run isolated tests and read-only live probes; deployment happens from the designated primary checkout after approval and required checks.

Protocol references: [QuickNode authentication](https://www.quicknode.com/docs/build-with-ai/quicknode-apis), [endpoint inventory](https://www.quicknode.com/docs/admin-api/endpoints/v0-endpoints), [endpoint creation](https://www.quicknode.com/docs/admin-api/endpoints/v0-endpoints-post).
