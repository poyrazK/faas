# ADR-20261030102813: A newer canary supersedes the one in flight

- **Status:** accepted
- **Date:** 2026-10-30
- **Decision:** When a canary deployment goes live while another canary of the same app and scope is still rolling out (`rollout_state` `pending` or `rolling_out`), the newer canary replaces it in the same transaction:
  - **The older canary leaves traffic.** It goes to `superseded` (or stays `live` at 0% when a revision pin, an active project release or durable work retains it, as `RetireLiveDeploymentSiblings` already does), and its rollout ends `aborted` with `rollout_aborted_reason` `superseded by deployment <id>`. Its share returns to the serving deployment, so the newer canary starts beside exactly one serving predecessor.
  - **Only when a serving deployment remains.** If the in-flight canary is the only deployment carrying traffic, it stays and is the newer canary's predecessor, as before.
  - **Every retirement ends the rollout.** A stable deployment going live, and a canary's terminal advance, retire live siblings. A retired sibling that was a canary still in flight now also ends `aborted` instead of keeping `rolling_out` on a superseded row.
  - **Signals.** The `rollout.aborted` app webhook fires for the older canary with that reason (the existing `deployments` trigger). schedd reaps its instances like any aborted canary candidate (`state.AbortedCanaryCandidate`), and the superseded status is published on `deployment_changed`.
- **Why:** ADR-1446 made every plain `gregale deploy` of a live app a canary, and meterd judges a candidate against its one serving predecessor (ADR-1446 "probe-backed low-traffic stages"; `cmd/meterd/canary_health.go`). Two deploys a few minutes apart therefore left a 98/1/1 split: two candidates beside the stable. meterd refused both every minute (`multiple serving predecessors`), so neither promoted. `release traffic promote` refused because a canary was in flight, and `release rollback --to <stable>` refused because the stable was already live. Only `release canary advance` on the newest, stage by stage, recovered it (Hunt 14 H14-112, production rc.304). Three revisions serving at once also exceeds the single rollout overlap that ADR-199's `RolloutConcurrencyGrant` budgets for.
- **Consequences:**
  - The newest deploy is the one that rolls out, which matches what a stable deploy already does to an in-flight canary.
  - A superseded canary is not an implicit rollback target, like any aborted canary candidate (H5-62): it never completed, so `release rollback` without `--to` returns to the last completed release.
  - Rows retired before this change keep `rolling_out` on a superseded row; they are inert, because every in-flight reader also requires `status = 'live'`.
- **Rejected alternatives:**
  - **Judging each candidate against the completed stable.** It would let both canaries promote. Whichever finished last would win, and the overlap would cost a second rollout grant for the whole rollout.
  - **Refusing the second deploy while a canary is in flight.** It would block hotfixes behind a slow, evidence-paced rollout.
