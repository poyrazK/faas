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
- Rules run in order; the **first match decides**. `accounts`, `group`,
  `plans` and `rollout` combine with AND. A rule needs at least one of them
  and an explicit `value`, so a rule can deny as well as allow.
- `rollout` is in basis points of the accounts the rule's other constraints
  admit: `1000` is 10%, `10000` is all. The bucket is a stable hash of the
  flag key and account, so widening a rollout keeps every account already in
  it.
- An account no rule matches gets `default`.
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

The environment and TOML values seed the flag at boot. Once an operator has
written the flag, they are ignored, including after a restart.

The last five flags were added after their environment gates had shipped.
Until an operator writes one of them, its existing gate still decides, and
`FAAS_APPLICATION_AUTH_*` is still re-read on every call. The first operator
write hands the decision to the flag.

Two of these flags have limits:

- `live_queries` is decided by account only, because the realtime path knows
  only the account. Plan rules never match it.
- Unless `live_queries` includes every account, the live-query routes stay
  hidden from accounts outside it.

Turning `anomaly_alerts` off refuses only new or changed baseline rules.
Existing rules keep evaluating. In the same way, turning
`trigger_batch_delivery` off refuses only creating a batch trigger or switching
one to batch. Triggers that already deliver in batches keep doing so.

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

gregalectl flags enable  --key ai_gateway --reason "open preview" --yes
gregalectl flags allow   --key ai_gateway --account-id <uuid> --reason "design partner" --yes
gregalectl flags deny    --key ai_gateway --account-id <uuid> --reason "abuse hold" --yes
gregalectl flags revoke  --key ai_gateway --account-id <uuid> --reason "back to rollout" --yes
gregalectl flags rollout --key ai_gateway --percent 10 --plans pro,scale --reason "widen" --yes
gregalectl flags default --key ai_gateway --value true --reason "general availability" --yes
gregalectl flags disable --key ai_gateway --reason "incident 123" --yes   # kill switch

gregalectl flags group-add --group design-partners --account-id <uuid> --reason "new partner" --yes
gregalectl flags groups

gregalectl flags history  --key ai_gateway
gregalectl flags rollback --key ai_gateway --version 3 --reason "incident 123 resolved" --yes
gregalectl flags set      --key ai_gateway --file flag.json --reason "reviewed change" --yes
```

`allow` and `deny` maintain two rules the CLI keeps first: `denied-accounts`
then `accounts`. An explicit decision therefore always beats a group, plan or
rollout rule; `revoke` removes the account from both.

The operator console's **Flags** page (`/operations/flags`) does the same
through the same API.

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
| `GET /v1/admin/flags/{key}/decision?account_id=` | Inspector: the decision, matching rule, reason and plan entitlement for one account |
| `GET /v1/admin/accounts/{id}/flags` | Every flag decided for one account |
| `PATCH /v1/admin/config/platform_flag.<key>` | Write a flag definition |
| `PATCH /v1/admin/config/platform_flag_groups` | Write the groups |
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

At most 32 rules per flag, 1,000 accounts per rule, 100 groups and 5,000
accounts per group (`pkg/api/limits.go`). Larger audiences belong in a group,
a plan constraint or a rollout.
