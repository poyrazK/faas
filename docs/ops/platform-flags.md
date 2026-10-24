# Platform flags

Platform flags release a Gregale platform feature to selected accounts
without a deploy or a restart (ADR-1700). Use them for previews, canaries and
staged rollouts: "turn the AI gateway on for these three design partners",
"release customer Flags to 10% of Pro accounts", "switch it off everywhere
now".

They are not customer Flags (`docs/flags.md`), which a customer uses to
release their own application's behavior to their own end users.

## What a flag is

Each flag is declared in `pkg/platformflags/catalog.go` and stored as a hot
runtime-configuration row, `platform_flag.<key>`. Its value is a definition:

```json
{
  "enabled": true,
  "default": false,
  "rules": [
    { "id": "denied-accounts", "accounts": ["…uuid…"], "value": false },
    { "id": "accounts", "accounts": ["…uuid…", "…uuid…"], "value": true },
    { "id": "partners", "group": "design-partners", "value": true },
    { "id": "rollout", "plans": ["pro", "scale"], "rollout": 1000, "value": true }
  ]
}
```

- `enabled` is the **kill switch**. `false` turns the feature off for every
  account, whatever the rules or default say.
- Rules run in order; the **first match decides**. `accounts`, `orgs`,
  `group`, `plans` and `rollout` combine with AND. A rule needs at least one
  of them and an explicit `value`, so a rule can deny as well as allow.
- `orgs` targets companies (ADR 20261021080640). It matches the organization
  a request acts under:
  - the active organization (`/v1/orgs/{slug}/…`, `X-Active-Org`);
  - the company project, app or managed Postgres database being used;
  - the organization an API key belongs to.

  The same person acting outside the company doesn't match. Use `accounts`
  to target a person.
- Not every flag can use every attribute. `gregalectl flags show` prints the
  flag's `attributes`. A write that uses any other attribute is refused with
  `409`, because that rule would never match where the flag is decided.
  - `ai_gateway` refuses `orgs`, because outboundd doesn't know the calling
    app's organization.
  - `live_queries` refuses `plans` and `orgs`, because the realtime path
    knows only the account.
- `rollout` is in basis points of the accounts the rule's other constraints
  admit: `1000` is 10%, `10000` is all. The bucket is a stable hash of the
  flag key and account, so widening a rollout keeps every account already in
  it.
- `starts_at` and `ends_at` (RFC 3339, stored to the second in UTC) give a
  rule a **schedule**: outside its window the rule is skipped, as if it were
  absent. Either bound may be left out, and a rule may have a window and no
  other constraint ("everyone from Monday 09:00 UTC"). The inspector names a
  scheduled rule that would have matched but is not yet started or has ended.
- An account no rule matches gets its **self-serve opt-in**, if it or its
  company joined the preview (see [Self-serve previews](#self-serve-previews)),
  and otherwise `default`.
- Accounts are account UUIDs. Groups live in one shared row,
  `platform_flag_groups`, so a list such as `design-partners` is maintained
  once for every flag.

A flag decides **release, never entitlement**. A Free account included by a
flag still cannot use a capability its plan lacks; the inspector reports both.

## Current flags

| Key | Releases | Consumers | Boot fallback |
| --- | --- | --- | --- |
| `ai_gateway` | AI gateway preview (ADR-998) | apid, outboundd | `ai_gateway_enabled`/`FAAS_AI_GATEWAY_ENABLED`, `ai_gateway_accounts`/`FAAS_AI_GATEWAY_ACCOUNTS` |
| `customer_flags` | Customer Flags (ADR-377) | apid | `flags_enabled`/`FAAS_FLAGS_ENABLED` |
| `application_auth` | Application Auth: the Auth screens and the `auth` credential mode (ADR-1190) | apid | `FAAS_APPLICATION_AUTH_ENABLED`, `FAAS_APPLICATION_AUTH_ACCOUNTS` |
| `live_queries` | Live queries (ADR-20261016151947) | apid | `FAAS_LIVE_QUERIES_ACCOUNTS` |
| `change_timeline` | App change timeline preview (ADR-751) | apid | `FAAS_CHANGE_TIMELINE_ENABLED` |
| `anomaly_alerts` | Baseline alert comparisons (ADR-744) | apid | `FAAS_ANOMALY_ALERTS_ENABLED` |
| `trigger_batch_delivery` | `delivery: batch` on triggers (ADR-1321) | apid | `FAAS_TRIGGER_BATCH_DELIVERY_PREVIEW_ENABLED` |
| `api_monetization` | API monetization for selected accounts (ADR-20261020170649); the `api_monetization_enabled` switch releases it to everyone. See `docs/ops/api-monetization-release.md` | apid, gatewayd-internal | none |

| `operation_schedules` | Operation schedules: routes and firing (ADR-20261018161833) | apid | `FAAS_OPERATION_SCHEDULES_ACCOUNTS` |
| `operation_children` | Child Operations and cancellation cascade (ADR-20261018183000) | apid | `FAAS_OPERATION_CHILDREN_ACCOUNTS` |
| `service_map` | Service map preview (ADR-1131) | apid | `FAAS_SERVICE_MAP_ENABLED` |
| `guest_tracing` | In-guest request tracing setting (ADR-958) | apid | `FAAS_GUEST_TRACING_ENABLED` (vmmd's receiver still needs it on the node) |
| `static_egress_ip` | Static egress IP management (ADR-119) | apid | `FAAS_STATIC_EGRESS_IP_ENABLED` |
| `realtime_resume` | v2 resumable realtime protocol (ADR-369) | apid → realtimed | `FAAS_REALTIME_RESUME_PREVIEW_ENABLED` |
| `realtime_tickets` | Realtime subscription tickets (ADR-1117) | apid → realtimed | `FAAS_REALTIME_TICKETS_PREVIEW_ENABLED` |
| `realtime_access` | Realtime principal access revocation (ADR-1113) | apid → realtimed | `FAAS_REALTIME_ACCESS_PREVIEW_ENABLED` |
| `realtime_occupancy` | Realtime channel occupancy (ADR-1324) | apid → realtimed | `FAAS_REALTIME_OCCUPANCY_PREVIEW_ENABLED` |
| `strict_budgets` | Strict compute and egress budgets (ADR-1750) | apid | none (off until an operator writes it) |
| `edge_waf_blocking` | Warn and block modes of WAF edge rules (ADR-20261023113721) | apid, gatewayd-internal | none (off until an operator writes it) |
| `edge_challenge` | Challenge edge rules, with difficulty escalation (ADR-20261023113721) | apid, gatewayd-internal | none (off until an operator writes it) |
| `edge_penalty_box` | Edge rule penalties (ADR-20261023113721) | apid, gatewayd-internal | none (off until an operator writes it) |
| `edge_attack_mode` | Attack mode, an app-wide managed challenge rule (ADR-20261021220456); switching it on also needs `edge_challenge` | apid, gatewayd-internal | none (off until an operator writes it) |

The environment and TOML values seed the flag at boot. Once an operator has
written the flag, they are ignored, including after a restart.

Every flag after `customer_flags` was added after its environment gate had
shipped.
Until an operator writes one of them, its existing gate still decides, and
`FAAS_APPLICATION_AUTH_*` is still re-read on every call. The first operator
write hands the decision to the flag.

Two of these flags have limits:

- `live_queries` is decided by account only, because the realtime path knows
  only the account. Plan rules never match it.
- Unless `live_queries` includes every account, the live-query routes stay
  hidden from accounts outside it.

The four realtime flags are decided by apid, which has no realtime node
database pool to share.

- **Delivery.** apid decides each endpoint's account and sends the decisions
  with the endpoint's registration. The 30-second endpoint reconcile replays
  every registration, so a change reaches realtime nodes within about 30
  seconds and applies to new connections.
- **Node switch.** A node's `FAAS_REALTIME_*_PREVIEW_ENABLED` stays a
  capability: a preview is on only where the node enables it and the flag
  allows the account.
- **Before any operator write.** apid sends no decisions, so registrations
  stay unchanged.
- **Upgrade order.** Upgrade every realtime node before writing any of these
  flags, because an older node refuses a registration that carries decisions.
  Retained messages (`FAAS_REALTIME_RETAINED_PREVIEW_ENABLED`) stay an
  environment switch.

Turning `anomaly_alerts` off refuses only new or changed baseline rules.
Existing rules keep evaluating. In the same way, turning
`trigger_batch_delivery` off refuses only creating a batch trigger or switching
one to batch. Triggers that already deliver in batches keep doing so.

`strict_budgets` is off until an operator writes it. While it is off for an
account, creating or enabling a strict (`suspend_workloads`) budget returns 422
`financial_budget_activation_unavailable` ("Strict budgets not released").
Strict budgets that are already enabled keep enforcing and stay editable, and
disabling or deleting one always works. Monitored budgets and alerts are not
gated. To release it to one account, run `flags enable` (the kill switch, with
the default still off) and then `flags allow` for that account.

`edge_waf_blocking`, `edge_challenge` and `edge_penalty_box` are off until an
operator writes them, and unlike `strict_budgets` they are kill switches at
the edge too. While one is off for an account:

- apid refuses creating a rule that uses the feature (any challenge rule, a
  WAF rule in warn or block mode, a rule with a penalty), and an update that
  switches the feature on for a rule that did not already enforce it
  (enabling the rule, leaving log mode, moving WAF to warn or block, adding a
  penalty), with 422 `edge_feature_unavailable`. Other edits, disabling and
  deleting always work.
- gatewayd-internal stops enforcing rules that already exist: challenge rules
  serve no page and the solve path goes to the app, warn and block WAF rules
  are inspected as observe, and penalties neither box nor refuse anyone. The
  rest of each rule keeps working (an `ip` rule with a penalty still refuses).

The gateway decides per request, so turning a flag on or off takes effect as
soon as the runtime-config watcher applies it (within its five-second repair
interval if a notification is missed), without an edge-rule cache reset.
Turning one on for an account that has inert rules makes them enforce at
once. Release them the same way: `flags enable`, then `flags allow` per
account, or `default` for everyone at launch.

## Day-to-day commands

Every write needs an operator session with a recent MFA step-up
(`gregalectl auth step-up`), a reason and `--yes`. The CLI reads the flag and
its version, edits it, validates it, and writes it back with that version; a
concurrent edit fails with a conflict instead of being lost.

```bash
gregalectl flags list                                   # every flag, groups, convergence
gregalectl flags show --key ai_gateway                  # definition + per-daemon receipts
gregalectl flags account --account-id <uuid>            # every flag decided for one account
gregalectl flags inspect --key ai_gateway --account-id <uuid>   # why on/off
gregalectl flags inspect --key application_auth --account-id <uuid> --org-id <uuid>   # ... acting under a company

gregalectl flags enable  --key ai_gateway --reason "open preview" --yes
gregalectl flags allow   --key ai_gateway --account-id <uuid> --reason "design partner" --yes
gregalectl flags deny    --key ai_gateway --account-id <uuid> --reason "abuse hold" --yes
gregalectl flags revoke  --key ai_gateway --account-id <uuid> --reason "back to rollout" --yes
gregalectl flags allow   --key application_auth --org-id <uuid> --reason "company pilot" --yes   # a whole company
gregalectl flags rollout --key ai_gateway --percent 10 --plans pro,scale --reason "widen" --yes
gregalectl flags default --key ai_gateway --value true --reason "general availability" --yes
gregalectl flags disable --key ai_gateway --reason "incident 123" --yes   # kill switch
gregalectl flags schedule --key ai_gateway --rule-id rollout --starts-at 2026-11-02T09:00:00Z --reason "launch day" --yes
gregalectl flags schedule --key ai_gateway --rule-id rollout --starts-at none --ends-at none --reason "unschedule" --yes
gregalectl flags inspect  --key ai_gateway --account-id <uuid> --at 2026-11-02T09:00:00Z   # decide as of a time

gregalectl flags group-add --group design-partners --account-id <uuid> --reason "new partner" --yes
gregalectl flags groups

gregalectl flags history  --key ai_gateway
gregalectl flags rollback --key ai_gateway --version 3 --reason "incident 123 resolved" --yes
gregalectl flags set      --key ai_gateway --file flag.json --reason "reviewed change" --yes
```

`allow` and `deny` maintain two rules the CLI keeps first: `denied-accounts`
then `accounts`. An explicit decision therefore always beats a group, plan or
rollout rule; `revoke` removes the account from both.

`schedule` sets or clears (`none`) a rule's window; a flag left empty keeps
its current bound.

The operator console's **Flags** page (`/operations/flags`) does the same
through the same API.

## Self-serve previews

An operator can let customers join a preview themselves (ADR
20261021080640):

```bash
gregalectl flags self-serve --key change_timeline --enabled true --plans pro,scale --reason "open preview" --yes
gregalectl flags self-serve --key change_timeline --enabled true --max-subjects 200 --reason "cap the cohort" --yes
gregalectl flags self-serve --key change_timeline --enabled false --reason "pause sign-ups" --yes
```

The definition's `self_serve` holds `enabled`, the eligible `plans` (empty
means every plan) and `max_subjects` (accounts plus organizations, at most
5,000; 0 means 5,000).

Customers join and leave with `GET /v1/previews`, `PUT /v1/previews/{key}`
and `DELETE /v1/previews/{key}`, or `gregale previews list|join|leave`. An
organization owner or admin joins for the whole company with
`/v1/orgs/{slug}/previews` or `--org`; members then get the preview while
acting for the company. A flag decided where the organization is unknown
(`ai_gateway`, `live_queries`, the two Operation flags) takes only account
opt-ins.

- Opt-ins live in their own hot row, `platform_flag_opt_ins.<key>`
  (`{"accounts": [...], "orgs": [...]}`), so a customer joining never
  rewrites the operator's definition. Every consumer receives it through the
  existing watcher, with history and rollback. apid writes it on the
  customer's behalf with the expected version, and the audit log records
  `platform_flag.opt_in` and `platform_flag.opt_out` with the customer as the
  actor.
- Opt-ins decide after the rules: a `value: false` rule listed first still
  excludes an opted-in account. `gregalectl flags deny` keeps working.
- `--enabled false` stops new opt-ins and ignores existing ones without
  deleting them. Customers can always leave.
- A preview never grants a plan entitlement. A flag that releases a paid
  capability (`ai_gateway`) must list only entitled plans; any other plan is
  refused with `409`.
- `gregalectl flags show` prints the opt-in counts, and `inspect` prints
  `opt_in=account` or `opt_in=org` when an opt-in decided.

## Automatic ramps

A rollout can advance itself (ADR 20261021080640). Create the rollout first,
then give it a ramp whose stages include the current percentage:

```bash
gregalectl flags rollout --key service_map --percent 1 --reason "start ramp" --yes
gregalectl flags ramp --key service_map --stages 1,10,50,100 --interval 30m \
  --max-5xx-percent 1 --min-requests 200 --on-breach rollback --reason "staged release" --yes
gregalectl flags ramp --key service_map --resume --reason "fixed the regression" --yes
gregalectl flags ramp --key service_map --remove --reason "hold at the current stage" --yes
```

Once a minute apid judges every unpaused ramp whose flag version is at least
`interval` old. It reads the 5xx share of the flag's **health routes** from
`apid_request_total` over that interval.

- With fewer than `min-requests` requests, or without Prometheus, it holds.
- Over the threshold it pauses, and with `rollback` it also returns one stage.
- Otherwise it advances one stage.

The write expects the flag's current version, so an operator change made
meanwhile wins and restarts the interval. The row's revision records no actor
and a reason naming the move. The audit log records
`platform_flag.ramp_advance`, `ramp_pause` or `ramp_rollback` as
`system:platform-flags-ramp`.

A paused ramp waits for `--resume`. `flags show` prints each ramp's stage,
settings and pause reason. Only flags whose catalog entry lists
`HealthRoutes` can ramp; a ramp on any other flag is refused with `409`.
The health series are not per cohort: a ramp judges the routes' overall
health.

## Retiring previews

A preview flag carries a `RetireBy` date in its catalog entry: the day it
should be released to everyone and its gate removed. `gregalectl flags list`
prints `retire_by=` and, once the date has passed, `overdue=true`. apid
exports `apid_platform_flag_retire_by_timestamp_seconds{flag}`, and
`FaasPlatformFlagPastRetireBy` opens a ticket for an overdue flag
([runbook](../runbooks/FaasPlatformFlagPastRetireBy.md)). Moving the date is a
catalog change with its reason in the PR.

## Propagation and convergence

A write updates `runtime_config_entries`. apid applies it in the request and
notifies `runtime_config_changed`. Every other consumer (outboundd today)
re-reads the table on that notification and every five seconds, so a change
is live everywhere within seconds even if a notification is lost.

Each consumer records an **apply receipt** per daemon and host in
`runtime_config_apply_receipts`. `gregalectl flags show` and the console list
them, and each flag reports its consumers as:

- **converged**: every instance of that daemon applied the current version;
- **pending**: an instance has not reported the current version yet (or the
  daemon has not reported at all);
- **failed**: an instance could not apply the current version. It keeps
  serving on its last valid value; the receipt names the error.

A flag nobody has written yet shows every consumer as converged on its boot
fallback.

The shared watcher also reports receipts for the other hot runtime settings
(HSTS, tenant surfaces, data placement, S3) in the daemons that consume them.

## API

| Route | Purpose |
| --- | --- |
| `GET /v1/admin/flags` | Every flag, its definition, version, receipts and convergence, plus the groups and which flags use each group |
| `GET /v1/admin/flags/{key}` | One flag |
| `GET /v1/admin/flags/{key}/decision?account_id=[&org_id=][&at=]` | Inspector: the decision, matching rule, reason and plan entitlement for one account, optionally under an organization and as of an RFC 3339 time |
| `GET /v1/admin/accounts/{id}/flags` | Every flag decided for one account |
| `PATCH /v1/admin/config/platform_flag.<key>` | Write a flag definition |
| `PATCH /v1/admin/config/platform_flag_groups` | Write the groups |
| `PATCH /v1/admin/config/platform_flag_opt_ins.<key>` | Write a preview's opt-ins (operators; customers use `/v1/previews`) |
| `POST /v1/admin/config/{key}/rollback`, `GET …/revisions` | History and rollback, as for every runtime setting |

The server rejects a rule that names an unknown group, and a groups write
that removes a group a flag still uses, with `409`.

## Adding a flag

1. Add a `Spec` to `pkg/platformflags/catalog.go` with the capability, the
   deciding ADR, its consumers, any legacy environment variables and, for a
   preview, a `RetireBy` date.
2. In each consumer, decide with `Set.Enabled(key, Subject{AccountID, Plan})`
   and keep the plan entitlement check next to it. apid uses
   `s.platformFlagEnabled`; another daemon runs a watcher through
   `runtimeconfig.WithPlatformFlags` with `Daemon` set so it reports
   receipts.
3. If the gate replaces an environment variable, seed the flag from it at
   boot and note that in the env contract.

## Limits

At most 32 rules per flag, 1,000 accounts and 1,000 organizations per rule,
100 groups and 5,000 accounts per group, 5,000 opted-in accounts plus
organizations per preview, and 2 to 10 ramp stages held 5 minutes to 7 days
each (`pkg/api/limits.go`). Larger audiences belong in
a group, a plan constraint or a rollout.

`allow`, `deny` and `revoke` take exactly one of `--account-id` or
`--org-id`. Their rules sit first, in this order: a person's deny, a person's
allow, a company's deny, then a company's allow. A decision about a person
therefore overrides one about their company.
