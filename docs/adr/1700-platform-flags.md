# ADR-1700 · Platform flags: per-account release gates managed at runtime

- **Status:** accepted
- **Date:** 2026-10-17
- **Relates to:** operator runtime configuration (`docs/ops/runtime-configuration.md`, cited as ADR-132 in code), ADR-143 (deploy configuration contract), ADR-377 (customer Flags), ADR-991 and ADR-998 (AI gateway release gate and staged rollout)
- **Decision:** Platform features are released to accounts by **platform flags**: catalogued, operator-managed gates that apply without a restart and can target individual accounts.
  - **Catalog.** `pkg/platformflags` declares every flag: key, label, the `pkg/productcap` capability it releases, the deciding ADR, the daemons that evaluate it, the legacy environment variables, and an optional retire-by date. Adding a flag is a code change.
  - **Storage.** A flag is a hot row in `runtime_config_entries` with key `platform_flag.<key>`. Shared account groups are one more row, `platform_flag_groups`. Both inherit what runtime configuration already does: optimistic versions, append-only revisions, rollback, reasons, MFA step-up, the operator allowlist, audit events and the `runtime_config_changed` notification.
  - **Definition.**
    - `enabled` is the kill switch. When it is false, the feature is off for every account, whatever the rules or default say.
    - Otherwise `rules` run in order and the first match decides. An account no rule matches gets `default`.
    - A rule's `accounts`, `group`, `plans` and `rollout` (basis points) combine with AND, and at least one must be set. `value` is required, so a rule can deny as well as allow.
    - The rollout uses the customer Flags hash (`flags.Bucket`) with a fixed platform seed. Widening a percentage keeps the earlier cohort.
  - **Evaluation.** Every consuming daemon holds a compiled, immutable snapshot. A decision is a map lookup with no I/O and no lock.
    - **apid** updates its snapshot inside the runtime-configuration manager, in version order with the other hot settings.
    - **The other daemons** update theirs through `pkg/runtimeconfig.Watcher`: on each notification, plus a 5-second repair pass.
    - **Fail closed.** An unknown flag, a missing account or a rule naming a missing group never matches.
  - **Apply receipts.** Every watcher, and apid, records in `runtime_config_apply_receipts` which version of each key it applied, or the error. The record is per daemon and per instance. "Applied" in the console then means applied where the flag is consumed, not only in apid.
  - **Entitlement boundary.** A flag decides release, never entitlement. A caller that gates a paid capability checks the flag **and** the plan. A flag that includes a Free account does not unlock a capability the plan lacks.
  - **Environment variables become the boot fallback.** A gate's existing variables seed version 0 of its flag. A durable operator value replaces it, and a restart never puts the environment value back.
  - **First flags.**
    - `ai_gateway` replaces `ai_gateway_enabled`/`FAAS_AI_GATEWAY_ENABLED` and `ai_gateway_accounts`/`FAAS_AI_GATEWAY_ACCOUNTS` in both apid and outboundd.
    - `customer_flags` replaces `flags_enabled`/`FAAS_FLAGS_ENABLED`.
  - **Gates added after their environment variables shipped.** Each of these keeps its existing gate until an operator writes the flag (version > 0); from then on the flag decides. Its environment value is also seeded as version 0, so the operator surfaces show what is in effect.
    - `application_auth`: `FAAS_APPLICATION_AUTH_ENABLED` and `FAAS_APPLICATION_AUTH_ACCOUNTS` (ADR-1190), still re-read on every call until then.
    - `live_queries`: `FAAS_LIVE_QUERIES_ACCOUNTS` (ADR-20261016151947). It is decided by account only, because the realtime path knows only the account. Unless it includes every account, the live routes stay hidden before routing.
    - `change_timeline`: `FAAS_CHANGE_TIMELINE_ENABLED` (ADR-751).
    - `anomaly_alerts`: `FAAS_ANOMALY_ALERTS_ENABLED` (ADR-744). Existing baseline rules keep evaluating while it is off; only creating or changing one is refused.
  - **Operator surfaces.**
    - Read endpoints:
      - `GET /v1/admin/flags`: every flag with its definition, version and receipts, plus the groups.
      - `GET /v1/admin/flags/{key}`.
      - `GET /v1/admin/flags/{key}/decision?account_id=`: the inspector.
      - `GET /v1/admin/accounts/{id}/flags`: every flag decided for one account.
    - Writes use the existing `PATCH /v1/admin/config/{key}` and `POST …/rollback`.
    - `gregalectl flags` wraps both.
    - The operator console gains an `/operations/flags` page (faas-frontend).
- **Why:**
  - **Most gates need a restart.** About 86 platform toggles are environment variables read once at boot. Changing one means an Ansible run and a restart.
  - **No per-account release.** Only two features can be released per account at all (ADR-998's AI list and the managed PostgreSQL canary list), and both are comma-separated environment variables.
  - **Lists drift.** ADR-998 recorded that the AI account list "must match in apid and outboundd". Two files had to be kept equal by hand, and the runbook described the symptom of a mismatch.
  - **No evidence of change.** The runtime-configuration console, the one live path, reported "applied" when apid had applied a value, even for keys only an edge daemon consumes.
- **Consequences:**
  - **For operators.**
    - Releasing the AI gateway or customer Flags to one more account is a `gregalectl flags enable` (or a console edit) that takes effect within seconds and is audited.
    - The kill switch turns a feature off platform-wide with no deploy.
    - The `FAAS_AI_GATEWAY_*` and `FAAS_FLAGS_ENABLED` variables still work until an operator writes a value. After that, editing them has no effect.
  - **outboundd now starts while the gate is off.** It no longer refuses to start when an operator AI integration is configured and the gate is off (ADR-991's startup check). The gate is now live, so a kill switch flipped during an incident would otherwise make outboundd crash-loop at its next restart. It logs a warning, and those integrations return `503 outbound_ai_gateway_disabled` per request, as they already did after a runtime change.
  - **outboundd now needs Postgres for the gate.** It reads `runtime_config_entries` and writes `runtime_config_apply_receipts` with its existing pool. While Postgres is down it keeps its last snapshot.
  - **Customer Flags is checked at runtime.** The rollout auto-advancer starts whenever the store supports it. It checks `customer_flags` on every pass and every account, so releasing Flags needs no restart.
  - **`GET /v1/capabilities` follows the flags.** A capability released by a platform flag is reported unavailable (`runtime_unavailable`) to an account the flag excludes. For customer Flags this corrects the earlier report of "available" while the gate was off.
  - **Bounds.** At most 32 rules, 1,000 accounts per rule, 100 groups and 5,000 accounts per group (`pkg/api/limits.go`). Larger audiences use a rollout or a plan constraint.
  - **Receipts grow with instances.** There is one receipt row per key, daemon and instance. They are rewritten only when a version changes or an apply fails, so the write rate follows operator changes, not traffic. Rows for a decommissioned host stay until they are deleted. The API shows their age.
- **Not migrated:**
  - **Managed PostgreSQL canary list.** `FAAS_MANAGED_POSTGRES_CANARY_ACCOUNTS` must equal the `canary_accounts` of a signed qualification approval. A live change would close all provisioning. Moving it needs the approval to name a flag version.
  - **Infrastructure switches** such as HSTS, data placement, tenant surfaces, S3, the domain doctor and the gateway retry/circuit breakers. They are global by nature. The first five are already hot runtime configuration. Their consumers now report receipts through the shared watcher.
  - **Deployment-managed variables.** Topology, listeners, secrets, billing mode and the daemon role stay environment variables.
- **Rejected alternatives:**
  - **The customer Flags tables (`feature_flag_versions`).** They are scoped to a customer's project environment, read by an SDK inside the guest over public HTTPS, and their customers are the tenant's own customers. They cannot reach outboundd or schedd, and mixing operator gates into tenant data crosses an ownership boundary.
  - **A new `platform_flags` table.** It would duplicate the versions, revisions, rollback, notification, audit and MFA that runtime configuration already has, and give operators two configuration consoles.
  - **An `account` scope in `runtime_config_entries`, one row per account.** Rollouts and groups cannot be expressed, rows grow with the customer base, and every daemon would have to load per-account rows.
  - **Evaluating in apid only.** Daemons would have to call apid on their request paths, which bypasses the component boundaries in CLAUDE.md and makes apid a dependency of outboundd's hot path.

## Evaluation

| Step | Result | Reason |
| --- | --- | --- |
| flag not in catalog or snapshot | off | `unknown_flag` |
| `enabled: false` | off | `flag_disabled` |
| no account on the request | off | `account_missing` |
| first rule whose accounts, group, plans and rollout all match | rule `value` | `rule_match` |
| no rule matches | `default` | `default` |

## Enforcement of the first flags

| Where | Check | Refusal |
| --- | --- | --- |
| apid, AI endpoints | `ai_gateway` kill switch | `403 outbound_ai_gateway_unavailable` |
| | `ai_gateway` for the account | `403 outbound_ai_gateway_unavailable` |
| | plan entitled | `402 plan_feature_gated` |
| outboundd, every AI call | `ai_gateway` kill switch | `503 outbound_ai_gateway_disabled` |
| | customer integration: `ai_gateway` for the account and plan entitled | `403 outbound_ai_gateway_unavailable` |
| apid, customer Flags management | `customer_flags` for the account | `403 forbidden` with a hint naming the cause |
| apid, `GET /v1/runtime/flags` | `customer_flags` kill switch, then for the workload's account | `403 forbidden` |
| `GET /v1/capabilities` | flag for the account | `runtime_unavailable` |
| apid, Auth screens, `auth` credential mode | `application_auth` for the account | unchanged from ADR-1190: screens hidden, `auth` treated as unknown |
| apid, live-query routes and realtime live routes | `live_queries` for the account | unregistered-route answer; no live routes offered |
| apid, `GET /v1/apps/{slug}/changes` | `change_timeline` for the account | `503 change_timeline_unavailable` |
| apid, alert rule create/update with a baseline comparison | `anomaly_alerts` for the account | `503 anomaly_alerts_unavailable` |
