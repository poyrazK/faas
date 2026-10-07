# Route requirements

Keep route configuration expectations in version control and check them against
a preview with `gregale preview report --requirements`. The check reads current
app metadata and edge rules; it does not send application requests or change
configuration. Findings show expected and actual configuration, matching rule
IDs, and review actions.

Use [route policy plans](route-policy-plans.md) to turn throttle and budget
violations into reviewable API patches with a complete requirements
recheck. Version 2 route groups support preview reports, policy planning, and
saved requirements checked against an explicitly selected deployment.

Create `gregale-routes.yaml`:

```yaml
version: 1
routes:
  - name: checkout
    method: POST
    path: /checkout
    require:
      authentication: consumer
      throttle:
        key_by: consumer_id
        max_rps: 10
        missing_key_policy: reject
      budget:
        explicit: true
        max_ms: 2000
```

```sh
gregale preview report pr-42-api --requirements gregale-routes.yaml
gregale preview report pr-42-api --requirements gregale-routes.yaml --json
gregale preview report pr-42-api --requirements gregale-routes.yaml \
  --format markdown --fail-on-requirements
```

The file contains one YAML or JSON document with `version: 1` and 1–500 routes,
up to 1 MiB. Unknown fields, duplicate method/path pairs or names, invalid
methods, and malformed bounds fail before platform access. At least one
requirement is needed per route. `name` is optional and methods are normalized
to uppercase.

## Route groups (version 2)

Version 2 requires an assignment for **every operation in the selected captured
candidate contract**. Groups apply automatically to new operations matching
their literal `path_prefix` and explicit methods. For example:

```yaml
version: 2
groups:
  - name: admin
    path_prefix: /admin/
    methods: [GET, POST, PUT, PATCH, DELETE]
    require:
      authentication: jwt
  - name: checkout
    path_prefix: /checkout/
    methods: [POST]
    require:
      authentication: consumer
      throttle: {key_by: consumer_id, max_rps: 10, missing_key_policy: reject}
      budget: {explicit: true, max_ms: 2000}
public:
  - method: GET
    path: /health
    reason: Public health endpoint
```

Run the same `preview report --requirements ... --fail-on-requirements`
command for either version. `/admin/` includes `/admin/users/{id}` and nested
descendants, but excludes `/admin` and `/administrator/users`. Use a concrete
entry in optional `routes` for the exact `/admin` operation. `/` selects all
captured paths for the stated methods. Methods are explicit; implicit HEAD
handling is outside this check.

Overlapping groups apply cumulatively. `public` exempts only the exact captured
method/path, including a supported whole-segment template; it does not alter or
prove runtime access. Every exception needs a nonempty reason, which stays in
the local file and is excluded from reports. An exact `routes` requirement may
not conflict with a public exception. A concrete `/orders/42` requirement does
not assign `/orders/{id}`. Empty groups are violations; exact assignments absent
from the capture are unknown, so stale exceptions cannot pass silently.

The inventory comes only from the captured candidate deployment and carries its
deployment ID and document SHA-256. It works when the baseline capture is
unavailable; removed baseline routes and currently observed policy routes are
excluded. Missing, empty, truncated, referenced or unparsed inventories are
unknown. TRACE operations and unsupported OpenAPI versions cannot establish a
complete inventory.
Non-root or variable effective OpenAPI server paths are unknown until their
mapping to gateway paths is established. Root/path/operation server overrides
are respected; raw server URLs are excluded from reports. Identical template
hierarchies with different parameter names also make inventory ambiguous.

Family checks support canonical decoded paths and nonempty whole-segment
parameters such as `{id}`. They prove invariant policy selection using literal
selectors, whole-segment `*`, and the gateway's recursive trailing `/*` behavior.
A higher/equal-precedence partial selector, conditional winning rule, priority
tie, request mutation, or unsupported syntax reports unknown. A lower-precedence
partial rule does not override a proven unconditional winner. Other globs can
still be checked for concrete paths. Embedded parameters, converters, encoded
aliases, dot segments, separators within parameters and framework normalization
are outside family proofs. Passing tests for one value cannot establish family
coverage.

Version 2 accepts at most 100 groups, 500 concrete routes and 500 public
exceptions within the existing 1 MiB document limit. It checks at most 2,000
candidate operations and 1,000 current rules, with bounded work and output. An
aggregate limit returns unknown coverage with no partial successful rows.
Version 1 concrete behavior remains available. Version 2 groups also support
[server policy planning and transactional apply](route-policy-plans.md#route-group-plans)
with an explicitly selected captured deployment contract. Public rationale stays
local: normalized policy intent uses the fixed `declared_public_exception` marker.

## Saved app requirements

Save version 2 requirements as the app's current route intent, then check each
deployment's captured contract against that intent and the app's current policy:

```sh
gregale routes requirements set my-api --requirements gregale-routes.yaml \
  --expected-revision 0
gregale routes requirements get my-api
gregale routes check my-api --deployment DEPLOYMENT_UUID \
  --expected-revision 1 --out route-check.json --fail-on-requirements --json
```

Every captured operation needs a group, exact requirement or public exception.
A new unassigned endpoint is a violation; missing or uncertain captured evidence
is unknown. `--fail-on-requirements` exits 1 for either, after writing the report.
Without that flag, successful report generation exits 0 even when findings exist.
The deployment must belong to the app. These checks compare its captured routes
with **current app configuration**, including auth, throttles and budgets. They
do not establish historical enforcement, runtime behavior or application-level
authorization.

The first save requires `--expected-revision 0`. Read the current revision before
replacing intent and pass it explicitly; a concurrent change returns a conflict.
Changed normalized intent increments the revision. Saving identical intent at
the correct revision retains its revision and update time. The optional check
pin fails if the current revision differs, so CI cannot silently use newer
intent. The server keeps the current intent; retain older versions in Git.

The check reads saved intent, current policy and selected capture in one
consistent snapshot. JSON binds the result to the app and deployment, saved
revision and intent SHA-256, configuration SHA-256, and captured-document
SHA-256 when available. Public rationale is replaced with the fixed
`declared_public_exception` marker before saving or transmitting from the CLI.
Keep the original explanation in your local requirements file.

Plan a repair directly from saved intent using the existing review and apply workflow:

```sh
gregale routes plan my-api --saved \
  --deployment DEPLOYMENT_UUID --throttle-burst 10 --out route-plan.json
gregale routes apply my-api --plan route-plan.json --confirm
gregale routes check my-api --deployment DEPLOYMENT_UUID --fail-on-requirements
```

Plan artifacts create a new file with owner-only permissions and refuse existing files
or symlinks. Saving intent needs app deploy-write scope; reading and checking
need app read scope. All three API operations use the existing MFA protection.
The explicit `routes check` command evaluates current evidence on demand.
Automatic checks run after capture or saved intent changes as described below.
These checks report findings without changing gateway policy. Canary advancement
can require passing evidence when the optional gate below is enabled.

## Automatic deployment checks

Apps with saved requirements receive an automatic check when a captured contract
is added, its bytes or truncation state changes, or the capture is deleted.
Saving new intent also queues existing captured deployments. Identical intent
and duplicate captures do not schedule extra work. The capture/intent write and
its durable queue handoff commit together; an apid worker retries failed work
and resumes expired leases after restart. Only the latest bounded result for
each deployment is retained, together with bounded completion history.

```sh
gregale routes results my-api --deployment DEPLOYMENT_UUID --json
gregale routes results my-api --deployment DEPLOYMENT_UUID \
  --expected-revision 1 --wait --out route-result.json --fail-on-requirements
```

The response separates queue state (`pending`, `running`, `retrying`, `complete`)
from freshness (`unavailable`, `current`, `stale`) and the stored check verdict.
A previously satisfied verdict can be stale. Each lookup reads current intent,
capture and configuration in one consistent snapshot and compares their
fingerprints with the stored evidence. Intent changes, changed/missing capture,
truncation changes and policy/configuration edits invalidate the corresponding
result. A missing or truncated capture completes with an unknown verdict.

Policy/configuration edits automatically queue fresh checks for live deployments
with retained captures or existing check jobs. Historical results still become
stale on lookup; explicitly refresh them when needed:

```sh
gregale routes results my-api --deployment DEPLOYMENT_UUID \
  --refresh --wait --timeout 2m --fail-on-requirements --json
```

Refresh coalesces with pending work and resets a failed retry. `--wait` polls
until the queue is complete, with a default 2-minute timeout and maximum of
10 minutes. A timeout exports the last validated result before exiting 1.
`--fail-on-requirements` requires **complete, current, satisfied** evidence;
pending, running, retrying, stale, unavailable, violated and unknown results
cannot pass CI. Reports are printed/exported before the gate exit. Without a
gate or wait timeout, fetching a valid result exits 0 regardless of findings.
The optional expected revision pins the current saved intent.

Reading/refreshing stored results requires app read scope, completed MFA and
current captured endpoint discovery entitlement. A plan downgrade cannot expose
historical captured routes through this API. Retry failures expose only the
stable `check_failed` code and next attempt time. Checks send no application
requests and change no route policy. An optional gate can use these results
before advancing an existing canary, as described below.
Saving requirements before any capture queues nothing; result lookup returns
404 until a capture or explicit refresh creates work.

## Finding changes and retained history

Inspect what changed in the latest completed check:

```sh
gregale routes results my-api --deployment DEPLOYMENT_UUID --changes --wait
gregale routes results my-api --deployment DEPLOYMENT_UUID \
  --refresh --wait --changes --out route-result.json
```

`--changes` displays new violations, resolved findings, changed policy values,
unknown findings and removed observations, with before/after actual values.
JSON/exported results include `check_id` and `changes` whenever comparison
evidence is available, including expectations and last-known check IDs. Pending
work can still contain a previous completed check; use queue state and freshness
before acting.

Comparison uses the last known observation for each method, path, requirement
and expectation under the same saved intent revision and hash. Unknown evidence
does not clear a known violation. Disappearing routes/checks are `removed`, not
resolved; a later violated reappearance can be a new violation. Initial checks
and changed saved intent show `observed` findings and establish a new baseline.
Only a confirmed passing finding under unchanged intent counts as resolved.
Changed rule order or explanatory wording alone stays quiet.

Comparison status is `initial`, `comparable`, `requirements_changed`,
`unavailable`, `ambiguous` or `tracking_limit`. Unavailable inventory/entitlement,
duplicate finding identities or excessive tracking size cannot establish
regressions or recovery. Detail is capped at 2 MiB; `truncated: true` explicitly
marks omitted finding rows while summary counts remain exact. A per-finding
`before_check_id` can precede `compared_to_check_id` after unknown checks.

Browse compact history summaries or fetch full immutable completion evidence:

```text
GET /v1/apps/{slug}/route-requirements/checks/{deployment}/history?limit=5
GET /v1/apps/{slug}/route-requirements/checks/{deployment}/history?limit=5&before={next_cursor}
GET /v1/apps/{slug}/route-requirements/checks/{deployment}/history/{check_id}
```

The newest-first list defaults to 5 entries and permits 1–10. Retention keeps at
most 20 entries and 64 MiB of encoded JSON per deployment, whichever is reached
first. Each full entry is capped at 32 MiB. Entries and cursors return 404 after
eviction. The independent last-known baseline can outlive retained history, so
eviction or repeated unknown checks do not produce duplicate regressions. There
is no backfill for checks completed before this feature.

History reads use the same ownership, read scope, completed MFA and current
captured endpoint entitlement as latest result reads. History preserves the
original check's provenance; it does not represent current policy or satisfy a
canary gate. Go, Node and Python SDKs expose list and exact-entry operations.

## Continuous monitoring and notifications

Live deployments with saved requirements receive fresh checks when edge rules,
consumer authentication, app execution budgets, rate limits or other effective
limit inputs change. Account plan and eligibility changes also invalidate work.
The policy write and durable queue request commit together. No-op writes and
unrelated app edits do not schedule checks; multiple edits retain one pending
job per deployment and fence workers using older inputs. A retained captured
deployment receives a fresh check when it becomes live again.

Subscribe to app webhooks for confirmed safety transitions:

```sh
gregale webhooks add --app my-api \
  --target-url https://ops.example.com/gregale/route-safety \
  --secret "$WEBHOOK_SECRET" \
  --event routes.requirements.violated \
  --event routes.requirements.recovered \
  --event routes.requirements.changed
```

A first confirmed violation emits `routes.requirements.violated`. Repeated
violated checks stay quiet when no additional finding becomes violated. A new
violation during an existing incident emits `routes.requirements.changed` for a
comparable check under unchanged saved intent. Initial/changed-intent
observations do not emit this additional event. A later satisfied check emits
`routes.requirements.recovered`; recurrence emits another violation. Unknown
evidence, failed checks and entitlement loss do not emit recovery or clear a
confirmed violation. An initial satisfied result has no recovery event.
Monitoring changes no policy or traffic and uses the current captured contract;
it does not establish runtime authorization.

Events contain metadata only:

```json
{
  "app_id": "APP_UUID",
  "deployment_id": "DEPLOYMENT_UUID",
  "status": "violated",
  "previous_status": "satisfied",
  "transition_id": "CHECK_REQUEST_UUID",
  "checked_at": "2026-10-02T10:00:00Z",
  "requirements_revision": 1,
  "requirements_sha256": "...",
  "configuration_sha256": "...",
  "capture_sha256": "...",
  "result_path": "/v1/apps/my-api/route-requirements/checks/DEPLOYMENT_UUID"
}
```

Notification intent commits with check completion. The existing webhook system
signs, retries and supports replay; delivery is at least once. Deduplicate the
signed delivery ID and use `transition_id` to correlate one safety transition.
Only enabled matching app subscriptions at that transition receive it. Later
subscriptions receive future transitions; account and platform-tenant receivers
do not receive these events. Existing app wildcard subscriptions include them.

The `routes.requirements.changed` payload adds `summary`, `comparison_status`
and `history_path`, identifying the exact retained check. Read `history_path`
before retention expires to inspect that notification's original evidence.

Read `result_path` with app read access and completed MFA, or inspect the same
result through the CLI. It returns the latest check, so compare its revision and
fingerprints with the event before acting. Route inventory and private intent
are excluded from notification payloads. Results retain affected routes, expected
versus actual policy, matching rules and next actions.

```sh
gregale routes results my-api --deployment DEPLOYMENT_UUID --wait --json
gregale routes plan my-api --saved \
  --deployment DEPLOYMENT_UUID --throttle-burst 10 --out route-plan.json
gregale routes apply my-api --plan route-plan.json --confirm
```

Use `--expected-revision N` to pin planning to the intent revision in a result
or notification. Saved plans automatically bind that revision and reject apply
if it changes. Review supported throttle/budget repairs with
[route policy plans](route-policy-plans.md#repair-from-saved-requirements); authentication and unknown findings
can require separate changes. A committed repair queues a check automatically.

## Canary route safety gate

Report mode is the default. Enable enforcement after saving requirements:

```sh
gregale routes gate get my-api --json
gregale routes gate set my-api --mode enforce --expected-revision 0
```

Use the gate's current revision when changing its mode. Its revision is separate
from the saved requirements revision. Enabling requires a plan with canaries and
captured endpoint discovery; app read scope can inspect the gate, while deploy
write scope and MFA are required to change it. Report mode can be restored after
a plan downgrade.

Every manual or worker canary advance requires a **complete, current, satisfied**
check when enforcement is enabled. The decision and traffic change share one
transaction, including locks on the current intent, app policy, plan and capture.
Missing or stale evidence requests a fresh check and returns 409
`route_gate_blocked` without changing traffic. Pending/running/retrying work
also blocks. Current unknown or violated evidence requires resolving the findings.

```sh
gregale routes results my-api --deployment DEPLOYMENT_UUID \
  --refresh --wait --fail-on-requirements --json
gregale deployment advance DEPLOYMENT_UUID --expected-step 1
```

Read the current deployment's canary step before advancing; each request advances
one stage and rejects a stale expected step. The worker treats a blocked gate as
a wait and retries on later ticks. API responses and successful deployment audits
contain the gate mode, revision, status and metadata-only reasons. Report mode
shows findings while allowing the advance.

The reason codes are `check_missing`, `check_incomplete`, `check_stale`,
`verdict_unknown`, `requirements_violated` and `evidence_unavailable`.
Legacy recovery `advance`/`promote` returns `use_canary_advance` under enforcement;
use the canonical command above. Generic traffic changes during active canaries
remain prohibited. Abort and stable rollback do not require route evidence.

```sh
gregale rollouts recover my-api --action abort --reason "route coverage needs repair"
gregale routes gate set my-api --mode report --expected-revision 1
```

This gate covers traffic increases for an already active canary. It does not
cover initial activation, first deployments or protected environment promotion,
and a candidate can already be receiving its initial canary traffic share.

## Results and CI

- `satisfied`: the inspected configuration meets the stated requirement within
  this check's scope.
- `violated`: known configuration differs from the requirement.
- `unknown`: unavailable data or request-dependent policy prevents a conclusion.

Without a gate, findings do not make report generation fail.
`--fail-on-requirements` requires `--requirements` and exits 1 for either
violations or unknown checks. It evaluates only the requested requirements;
missing latency or test evidence elsewhere in the report does not trigger this
gate. The report is written before the gate exit.

The JSON report adds an optional `requirements` section with version 1 or 2, a
SHA-256 fingerprint of the supplied file bytes, the inspected hostname,
`policy_scope: current_app`, and each check's status, stable code, expected and
actual summaries, rule IDs, reason, and next action. A known requirement
violation produces `outcome: policy_violations` unless a contract break or test
failure takes precedence. Unknown requirements keep the report incomplete.
The existing `--fail-on-incomplete` also covers requirement findings.
Version 2 also includes `coverage` provenance, group summaries and per-operation
`assignments`. Each assignment identifies groups and their `check_index` values
in that operation's `checks` array. Unassigned operations, empty groups and
unavailable inventory appear in review priorities. The outer report remains
version 4.

## Authentication

`authentication: consumer` requires the app's `consumer_auth_mode` to be
`required`. An account bearer token, IP restriction, or JWT rule is not a
substitute for this particular consumer identity requirement. This setting is
app-wide; remediation can affect every route.

`authentication: jwt` requires a valid, enabled JWT rule selected for the
request's host, method, and path. The check validates the configured action;
it does not fetch JWKS or verify a token.

`authentication: application` reports `unknown`, since platform configuration
cannot establish authentication implemented by application code. Use
customer-defined assertions in `gregale test` for that evidence. None of these
checks establishes business authorization, ownership, or a tenant boundary.

## Throttles

`throttle.key_by` is required when specifying a throttle requirement and supports
`none`, `api_key`, `consumer_id`, and `jwt_subject`. Optional `max_rps` must be
positive and bounds the selected rule's gateway-effective rate, including the
gateway's defensive minimum. Optional `missing_key_policy` is `shared` or
`reject` and requires a dimensional key.

A shared limiter does not satisfy `consumer_id`, even if a lower-precedence
consumer limiter also matches. An outer app/account limit alone does not
satisfy a route throttle requirement. The check does not inspect live buckets
or predict admission/HTTP 429. Dimensional limits retain their existing bounded
overflow behavior; they do not guarantee an unlimited number of independent
customer buckets.

## Execution budgets

`budget.explicit: true` requires a selected route budget rule or a positive app
`request_timeout_s`; a plan baseline alone is insufficient. Optional positive
`budget.max_ms` compares the resolved configured baseline after the same plan
and platform clamps used by edge-rule tracing. Specify at least one of these
fields.

This is the guest-execution budget baseline for requests without a budget
header override. A permitted request header can alter the runtime budget. The
result does not establish an absolute limit across all request contexts or an
end-to-end response-time SLO: upload, wake, routing, and admission occur before
the guest budget begins.

## Scope and uncertainty

The first version inspects concrete, decoded paths on the preview app's
platform hostname from `app.url`. Query strings, fragments, encoded aliases,
dot segments, route placeholders (`{id}` or `:id`), and globs are rejected.
For `/products/{id}`, a requirement on `/products/example` checks that concrete
path only. It does not establish policy coverage for every possible ID. Custom
domains and named project-environment policies need separate scoped evaluation.

Host, method, and path selection reuse edge-rule tracing's matcher semantics.
Lower numeric priority wins. Equal-priority candidates and a highest-precedence
header-dependent candidate report `unknown`. A lower-precedence conditional
rule cannot displace an unconditional winner. Routing, rewriting, and request
header mutations report uncertainty for checks whose selection they can affect.
Response-only header changes do not affect that selection.

Missing or inaccessible app/rule data and invalid candidate actions also remain
unknown. The report checks current app configuration, not a historical policy
snapshot bound to the selected deployment. It does not prove that a route
exists or returns successfully. Raw rule actions, JWT configuration values,
and selector-header values are excluded from the report.

See [route change reports](route-change-report.md) for contract, test, and
traffic evidence, and [edge-rule tracing](edge-rule-trace.md) for concrete
request policy investigations.

## Observed application route health

For candidate/stable comparisons of actual observed 5xx responses on critical
routes, see [critical route health](route-health.md). Its independent opt-in guard
complements configured policy requirements and shares the atomic canary advance
path. The route health report explains sample counts and telemetry limitations.
