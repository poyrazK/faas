# ADR 20261022153155 · AI gateway: AI signals per deployment and in release gates

- **Status:** accepted
- **Date:** 2026-10-22
- **Relates to:** ADR-009 (identical inner network world), ADR-990 (AI usage settlement), ADR-1340 (route canaries), ADR-20261019071734 (release gates on customer signals), ADR 20261021171712 (quality signals)
- **Context:**
  - The AI gateway counts usage by integration, caller app, model, tag, end user and trace. It does not know which of an app's deployments made a call.
  - So a deploy that changes a prompt, a model or a retry loop cannot be told apart from the one before it. Its cost per call, its provider errors and its flagged answers are mixed into the app's totals.
  - Release gates (ADR-20261019071734) judge a canary and the post-release watch on SLO burn, new error groups and custom metrics. A release that doubles its AI cost per call, or whose answers start coming back truncated or refused, passes every one of them.
  - An app's calls already carry a verified workload identity minted by vmmd, with the calling instance's ID. The instance row names its deployment.
- **Decision:**
  - **Attribution.** A settled AI call from an app instance adds to its deployment's row for the current UTC minute in a new table, `outbound_ai_deployment_minutes`:
    - Keyed by (deployment, minute), with the app. It counts calls, provider errors, calls with a quality flag, cost, input and output tokens, and the calls with a first byte and their summed time to first byte.
    - The settlement resolves the deployment in the same statement, from `instances` by the identity's instance ID and app. No extra round trip, and no row when the instance is gone.
    - Only calls with a verified instance count: the relay identity and the workload identity header. Access tokens, Runs, workflows, replays, the gateway's own calls and responses the cache served have no deployment and are not counted.
    - The table has no foreign keys, like the route minutes (ADR-1340), so a settlement never fails on it. Rows older than `OutboundAIDeploymentStatsRetentionDays` (14) are pruned with the other AI rollups.
  - **A fourth gate kind, `ai`.** It reuses the gate table's columns:

    | Column | `ai` gate |
    | --- | --- |
    | `metric_name` | `error_rate`, `flagged_rate`, `cost_per_call` or `ttfb_ms` |
    | `threshold` | The largest increase over the baseline, in percent (1–1000) |
    | `min_samples` | Calls each side needs before the gate is judged (time to first byte counts calls with a first byte) |

    - The gate fails when the release's value is above the baseline's by more than the threshold percent, and by more than a floor: 1 percentage point for the rates, 50 ms for time to first byte, 1 millicent for cost per call. The floor keeps a near-zero baseline from failing on noise.
    - The baseline is what the callers already pass: the stable deployment over the same window for a canary, and the predecessor before the rollout for the post-release watch. A release without a baseline, or either side below `min_samples`, is not judged.
    - The result reports the release's value as observed, the failing value as the bound, and both values in its summary.
  - **Visibility.** `GET /v1/apps/{slug}/ai-deployments` lists an app's deployments with AI calls in the retention window, newest first: each one's totals, rates, cost per call and mean time to first byte. The CLI shows it as `gregale ai deployments`, and `gregale release-gates add --ai METRIC --max-increase PCT` adds an `ai` gate.
  - **Schema change.** `app_release_gates.kind` gains `ai`, and a new CHECK holds the `ai` gate's columns. The kind CHECK is replaced online and keeps its name, as the edge-rule outcome CHECK was: a wider copy is added `NOT VALID` and validated, the old one dropped and re-added wider, then the copy dropped.
- **Why:**
  - **Minute rows per deployment** answer both a canary's window and a release's lifetime with one indexed range read. The request log has no deployment, is optional per integration, and is sampled for content.
  - **The instance is the only honest source of the deployment.** Guests share one inner network world (ADR-009), so nothing on the wire names the deployment, and a guest-supplied header could name any. vmmd already signs the instance into the identity, and `instances` maps it to the deployment that booted it.
  - **Relative bounds** fit AI signals better than absolute ones. A sensible cost per call or error rate differs by orders of magnitude between apps; a release making them worse than its predecessor is the regression a gate exists to catch.
- **Consequences:**
  - Each settled app call writes one more upsert in its settlement transaction. Calls of one deployment in one minute share a row, as calls of one caller and model in a day already share the usage rollup row.
  - A call whose instance row is gone at settlement, such as a stream that outlives its instance, is not attributed.
  - An `ai` gate is judged only when both sides call the AI gateway. A release that stops calling it is not judged.
  - Release N-1 skips `ai` gates when it gathers evidence, and its apid refuses a rollout decision carrying an `ai` result. Only an apid that accepts the kind can create such a gate, so an `ai` result reaches an N-1 apid only after that apid is rolled back; meterd then retries the decision on its next tick, as it does any refused report.
- **Rejected alternatives:**
  - **A deployment header set by the guest.** Unverifiable, and runner shims would have to forward it on every call.
  - **Reading the deployment from the request log.** It is optional, keeps no deployment, and would need a scan per gate evaluation.
  - **Absolute thresholds per AI metric.** Each app would have to know its own normal values; a percentage over the predecessor works without tuning.
  - **Counting cache hits.** They have no cost or provider latency, so a release whose prompts stop matching the cache would look better per call, not worse.
