# ADR-973: Managed runtime update windows and runtime family lifecycle

Status: proposed · 2026-10-10

## Context

ADR-736 records exact runtime identity and read-only upgrade previews, and
states that it is "not Beanstalk-style scheduled updates". The private chain
that followed can now rebuild a pinned candidate and cut it over safely:
build targets (ADR-737), reviewed baselines (ADR-738), native qualification
receipts (ADR-739, ADR-815, ADR-817), candidate acceptance (ADR-816), atomic
cutover (ADR-689), the durable executor and worker (ADR-690, ADR-691),
reservation and cancellation (ADR-692) and gateway verification and drain
(ADR-693 to ADR-697). Previews still report `execution_available=false`.
Nothing decides *when* an app is updated, nothing lets a customer opt in, and
nothing tells a customer that their runtime family is losing upstream support.

Comparable platforms treat this as table stakes. Elastic Beanstalk applies
minor and patch platform updates inside a customer-chosen weekly window,
through an immutable deployment, with rollback on health regression and a
"managed actions" history. It also publishes a supported → deprecated →
retired schedule per platform branch. Gregale customers today must redeploy
to pick up a patched base and get no signal before a family such as `node22`
stops receiving releases.

Unscheduled platform change also costs latency. On 2026-10-09, prod cold boots
with reason `snapshots_stale` clustered around platform deploys: each deploy
re-cold-boots every app once. An update that rebuilds an app's artifact
invalidates its snapshots in the same way, so update timing is visible to
customers as first-request latency.

## Decision

Add a customer-facing update policy that drives the existing private executor
on a schedule. Add a lifecycle state to each runtime family. The scope is the
six managed function families covered by ADR-736. Dockerfiles, OCI images,
ordinary apps, Jobs and sidecars remain outside it (see Consequences).

### Update policy

Each app has an update policy that inherits an account default:

| Field | Values | Default |
|---|---|---|
| `mode` | `off`, `notify`, `auto` | `notify` |
| `window` | weekday, start time, IANA timezone | platform-assigned |
| `window_duration` | 2h to 8h | 4h |
| `soak` | minimum age of a target release before it is eligible | 72h |

- `off` records nothing and sends nothing.
- `notify` emits an event when an eligible target exists, and allows a manual
  apply.
- `auto` applies the eligible target inside the next window.

At launch the default is `notify` for every app. Changing the default to
`auto` is a separate explicit ADR, following the default-flip precedent in
ADR-052.

Windows are interpreted in the configured timezone. A start time in a DST gap
moves to the next valid minute. A repeated hour uses its first occurrence.
Pro and Scale can set the window. Free and Hobby receive a platform-assigned
window, derived from a stable hash of the app ID and spread across the
platform's low-traffic hours. It is visible to them but cannot be edited. The
entitlement is a new `UpdateWindowConfigurable` bool in `pkg/api/limits.go`.

### Target selection

An update moves an app only *within* its current family and architecture.
Changing family (`node22` → `node24`) remains an explicit application
migration, as ADR-736 requires, and is never automatic. A release is an
eligible target only when all of these hold:

- it belongs to the same family and architecture as the serving release;
- it holds an unrevoked native qualification receipt (ADR-739);
- it has been published for at least `soak`, so internal apps adopt it first;
- no `rolled_back` record exists for this app and target.

When several releases qualify, choose the most recently qualified one.
Publication order alone never ranks targets (ADR-736).

The update is skipped with a fixed reason code when the app:

- has unknown provenance;
- has a deployment or rollout in progress, or a protected-promotion freeze;
- has a cancelled candidate still awaiting cleanup (ADR-692);
- is already serving the target.

### Execution

At window start, a scheduler loop in the opt-in apid runtime-upgrade worker
(ADR-691) creates one ADR-692 reservation per eligible `auto` app. From there
the existing chain runs unchanged: exact source staging, a pinned build, a
fresh cold-boot prime, gateway verification and atomic cutover. The previous
release stays at zero traffic for rollback.

- **Window end.** Cutover may commit only while the window is open. A
  reservation still short of cutover at window end is cancelled through the
  ADR-692 fences and retried in the next window.
- **Health rollback.** Health regression after cutover triggers the existing
  health-driven rollback. The pair (app, target) is then recorded as
  `rolled_back` and is never automatically retried. A newer target can still
  be selected later.
- **Parked apps.** Parked apps are updated without waking for traffic. The
  prime is the only boot.
- **Billing.** Platform-initiated build and prime time is not billed as
  GB-RAM-hours.
- **Capacity.** Concurrent update builds are capped fleet-wide and per node.
  The cap uses the existing builder admission. Apps that miss a slot move to
  the next window and are recorded as `deferred`.
- **Apply now.** A manual apply runs the same pipeline immediately, outside any
  window. It is permitted in both `notify` and `auto` modes. This is the
  customer apply control that ADR-736 leaves out.

### Update history

Every operation produces a customer-visible record with these states:
`scheduled`, `deferred`, `skipped`, `running`, `succeeded`, `rolled_back`,
`cancelled`, `failed`. Each record carries a reason code, source and target
release IDs, the window it ran in, and the resulting deployment ID. Each state
transition is emitted as a `runtime_update.*` event through the existing app
and [account release webhooks](../account-release-webhooks.md) and the audit log.
A `scheduled` record is created at least 24 hours before its window, so
customers can see and defer the next update.

### Runtime family lifecycle

Each runtime family carries a lifecycle state and dates in a checked-in
catalogue: `supported` → `deprecated` → `retired`. The catalogue is generated
into the customer docs, like `docs/capabilities.md`.

- **Deprecated.** Deploys succeed. The API returns a `warnings[]` entry, and
  the CLI and console show the retirement date. Deprecation must be announced
  at least 90 days before retirement. A `runtime_family.deprecated` event fires
  for every affected app.
- **Retired.** New apps and new deployments on the family are rejected with
  `runtime_retired`, and the error names the suggested successor family.
  **Existing deployments keep running, waking, restoring and cold-booting.**
  Retirement never stops a customer workload. No new releases are published for
  the family, so `auto` apps simply stop receiving updates.

Revoking a single release's qualification (ADR-739) is separate from family
lifecycle. When the serving release is revoked, a notification is sent
immediately and, for `auto` apps, the update runs in the next window.
Out-of-window remediation remains an operator break-glass action.

### Surfaces

- REST:
  - `GET/PUT /v1/apps/{slug}/update-policy`
  - `GET/PUT /v1/account/update-policy`
  - `GET /v1/apps/{slug}/runtime-updates` (history)
  - `POST /v1/apps/{slug}/runtime-updates` (apply now)
  - `GET /v1/runtimes` (families and their lifecycle)
- CLI:
  - `gregale app <slug> updates` shows policy, next window, pending target and
    history.
  - `gregale app <slug> updates set --mode auto --window "sun 03:00 Europe/Istanbul" --duration 4h`
  - `gregale app <slug> updates apply`
  - `gregale account updates set ...`
- Console: the existing runtime tab gains policy, next window and history.
- Product registry: a `managed-runtime-updates` capability in
  `pkg/productcap/catalog.json` at maturity `internal`. Preview responses
  report `execution_available=true` only for apps where the capability is
  enabled.

## Consequences

- New append-only migrations add account and app update policies, update
  operation records and the family lifecycle catalogue. The executor's
  existing tables stay the source of truth for execution state; update records
  reference them and do not duplicate them.
- Every update rebuilds the artifact, so it invalidates the app's snapshots.
  The first request after an update pays a cold boot unless the prime's
  snapshot is published first. Placing this inside a window is the point of
  the window. Platform deploys that invalidate snapshots (`snapshots_stale`)
  should follow the same window in a follow-up ADR. That would turn the
  current churn after every platform deploy into scheduled, expected
  behaviour.
- Builder load becomes periodic and bursty around popular window times. The
  fleet cap and the spread of platform-assigned windows bound it; `deferred`
  makes overflow visible instead of silent.
- Coverage stays limited to managed function families. Customer Dockerfiles
  and OCI images get no automatic rebuild, because Gregale does not own their
  base. A follow-up may let `notify` mode report base-image CVEs for them from
  the existing scan sidecars.
- Kernel, function-runner and guest-init upgrades remain platform deploys, as
  in ADR-736, and are not customer-scheduled by this ADR.
- Promotion beyond `internal` requires native end-to-end acceptance of an
  `auto` update of a parked app followed by a wake. It also requires the
  outstanding ADR-692 to ADR-697 gateway drain acceptance.

## Rejected alternatives

- **Apply every qualified release immediately, without windows.** This
  surprises customers, creates load spikes on every publication, and turns
  each release into fleet-wide cold boots at an arbitrary time.
- **Update in place by replacing the logical base.** This is the pre-ADR-736
  behaviour. It breaks exact rollback and snapshot backing identity (ADR-510).
- **Semver-style "latest patch" targets.** ADR-052 and ADR-736 forbid inventing
  language patch versions; qualified release IDs are the only target identity.
- **Automatic family upgrades.** Changing family alters language semantics.
  Beanstalk also excludes major platform-version changes from managed updates.
- **Stopping workloads on retired families.** This breaks customer production
  to enforce a deprecation. Blocking new deploys is enough pressure, and it is
  reversible.
- **Updating on the next wake.** This would put a rebuild on the wake critical
  path, which contradicts the scale-to-zero latency budget.

## Validation

- **Unit tests:**
  - window arithmetic across timezones, DST gaps and repeated hours;
  - target eligibility for every skip reason;
  - soak and rolled-back exclusion;
  - an idempotent scheduler tick, including restart mid-window;
  - cancellation at window end;
  - fleet-cap deferral;
  - `runtime_retired` rejection, while existing artifacts still wake.
- **API and CLI tests:** policy inheritance, plan entitlement errors, history
  pagination and webhook payloads.
- **Native acceptance:** `test-metal` and `leakcheck` on a native x86_64 KVM
  host, plus an end-to-end auto update of a parked managed function, rollback
  on an injected health regression, and a post-update wake.
