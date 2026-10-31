# ADR-20261030151810: Apps and releases report conditions, and a wait reads them

- **Status:** proposed
- **Date:** 2026-10-30
- **Extends:** the app health assessment (`pkg/apphealth`, `GET /v1/apps/{slug}/health`)
  and `gregale release wait`; it changes neither.
- **Checked against:** ADR-20261026145832 (CLI command tree: no new top-level
  verb, `status` for live state, `wait` a standard verb), ADR-20261023115741
  (every capability behind a platform flag), `docs/release-compatibility.md`
  (release N beside N-1), and the component ownership rules in `CLAUDE.md`
  (apid writes no scheduler or deployment state).

## Context

A person or an agent that changes something on Gregale then has to find out
whether it worked. Every resource answers that differently:

- **Waiting.** `cmd/gregale/cli_meta.go` declares 57 `wait` flags or commands
  with about 45 distinct meanings: "wait for the deployment to become live",
  "wait for a fresh healthy default-scope assessment", "wait for the requested
  wake to reach running", "follow the run; exit 0 only if it succeeds (1 failed
  or cancelled, 3 timeout)". Exit codes differ too: `job wait` uses 0, 1 and 3;
  `release wait` exits 1 on both failure and timeout.
- **State.** An app reports `status` (`active`, `evicted_cold`, `deleted`, or
  the read-model `undeployed`) and, separately, `deployment_availability`
  (`live`, `no_live_deployment`). A deployment reports `status` (`pending`,
  `building`, `imaging`, `snapshotting`, `live`, `failed`, `superseded`,
  `cancelled`) plus rollout fields. Neither says, in one place, "is it serving,
  is something still changing, and if not, why".
- **Evidence exists.** `pkg/apphealth` already assesses an app from existing
  evidence without waking or probing it: an overall `status` (`healthy`,
  `degraded`, `unhealthy`, `unknown`), a `phase` (`serving`, `idle`,
  `starting`, `deploying`, `not_deployed`, `maintenance`, `stopped`,
  `unsupported`) and coded checks (`deployment`, `latest_deployment`,
  `readiness`, `traffic_readiness`, `requests`, `maintenance`, `workload`).

What is missing is one shape for that answer, the same on every resource, and
one way to wait for it. Kubernetes solved the same problem with
`status.conditions` and `kubectl wait --for=condition=Ready`; most agents and
many developers already know that shape.

## Decision

### 1. A condition

```json
{
  "type": "Ready",
  "status": "False",
  "reason": "readiness",
  "message": "0 ready, 2 unready replicas. Required probes or node availability report a failure.",
  "since": "2026-10-30T15:02:11Z",
  "terminal": false
}
```

- **`type`** is a fixed name per resource kind (decision 3).
- **`status`** is `True`, `False` or `Unknown`, as in Kubernetes.
- **`reason`** is a stable `snake_case` code from a closed list in `pkg/api`,
  safe to branch on. **`message`** is for people and may change.
- **`since`** is the time the condition last changed, only when a recorded
  timestamp says so; it is omitted rather than guessed.
- **`terminal`** is Gregale's addition: `true` when the condition will not
  change without a new request (a failed deployment stays failed). A wait stops
  on it instead of running to its timeout. It is `omitempty`.

### 2. A status subresource, derived on read

Each kind gets `GET …/status` returning
`{"kind", "name", "observed_at", "conditions": [...]}`:

- `GET /v1/apps/{slug}/status` (with `?env=` as `/health` takes it)
- `GET /v1/deployments/{id}/status`

Conditions are **computed when read, from evidence apid already reads**, and
never stored:

- No table, column or writer is added. schedd stays the only writer of
  instances, and deployment state keeps its owners.
- The app status runs the same bounded collection as `/health`
  (`apphealth.CollectScope`, `api.AppHealthCollectionTimeout`), so it never
  calls schedd, vmmd or the app, and never wakes it. It is not on the request
  path; `Cache-Control: no-store`.
- The deployment status is mapped from the same `api.DeploymentResponse`
  `GET /v1/deployments/{id}` returns, under the same scopes.
- The mapping lives in one package of pure functions over API types, so the
  server, the CLI and tests share it.

Conditions are not added to `GET /v1/apps/{slug}`: the health collection reads
metrics, which an app list or detail read must not pay for.

### 3. The first conditions

**App** (from the health assessment):

| Type | `True` | `False` | `Unknown` |
|---|---|---|---|
| `Ready` | no check fails or is unknown; reason is the phase (`serving`, `idle`, `starting`) | phase `not_deployed`, `deploying` with nothing serving, or `maintenance` (reason is the phase); phase `stopped`, a service scaled to zero (terminal); a failing check (reason is its code). Terminal when the latest deployment failed and nothing serves. | an unknown check (reason is its code); phase `unsupported` (reason `not_assessed`) |
| `Progressing` | a deployment in the scope is in progress (reason `deploying`) | otherwise: reason `latest_deployment_failed` or `complete` | the deployment history is unknown |
| `Degraded` | health `degraded` or `unhealthy`; reason is the worst check's code | health `healthy` | health `unknown` |

`Ready` reads every check except `requests`. Request telemetry says how
traffic went, not whether the app can serve, and plans without per-app metrics
never have it; a failing or unknown `requests` check shows in `Degraded`. So an
idle app that scales to zero is `Ready=True reason=idle` on every plan: the
next request wakes it, and that is the product working, not a failure.

**Deployment** (from its status and rollout fields):

| Type | `True` | `False` |
|---|---|---|
| `Ready` | `live` and its readiness stage completed (reason `live`) | live while readiness checks still run (reason `verifying`; the release is routable but not yet confirmed, as `release wait` already treats it); `pending`, `building`, `imaging`, `snapshotting` (reason is the status); `failed`, `cancelled`, `superseded` (reason is the status, terminal) |
| `Progressing` | `pending`, `building`, `imaging` or `snapshotting` (reason is the status), live while readiness runs (reason `verifying`), or live with a rollout still advancing (reason `rolling_out`) | otherwise (reason `complete`, or the terminal status) |
| `RolledOut` | live and the rollout reached 100% traffic, or no safe rollout (reason `rolled_out`) | rolling out (reason `rolling_out`); rollout aborted (reason `rollout_aborted`, terminal); live at 0% in the default lane, as `deploy --no-traffic` leaves a release until it is promoted (reason `staged`, amendment 1); not live (reason is the status, terminal when `Ready` is) |

**Database** (`GET /v1/postgres/databases/{id}/status`, from the database
response with its cached health):

| Type | `True` | `False` | `Unknown` |
|---|---|---|---|
| `Ready` | state `ready` (reason `ready`, or `suspended` when compute scaled to zero: the next connection wakes it) | `provisioning`, `updating` (reason is the state); `failed`, `deleting`, `deleted` (reason is the state, terminal) | — |
| `Progressing` | `provisioning`, `updating`, `deleting` (reason is the state) | otherwise (reason `complete`, or `failed`, terminal) | — |
| `Degraded` | cached health `degraded` (reason is its error code, else `degraded`) | cached health `healthy` | health off, stale or not yet checked (reason `not_assessed`, `stale`) |

`Ready` follows the lifecycle state, not the cached provider health: health
is a cached observation, not a connectivity proof, so it shows in `Degraded`.

**Domain** (`GET /v1/domains/{domain}/status`, from the durable domain row;
the read never dials the domain, unlike `GET /v1/domains/{domain}`):

| Type | `True` | `False` | `Unknown` |
|---|---|---|---|
| `Verified` | the ownership TXT record was found (reason `verified`) | not yet (reason `dns_pending`) | — |
| `CertificateReady` | certificate `issued`, `renewing`, or served by a CDN (reason is the status) | `pending`, `failed`, `dns_drifted` (reason is the status) | no recorded status (reason `not_assessed`) |
| `Ready` | both of the above (reason `serving`) | the first that is not: its reason | — |

None is terminal: the DNS poller retries, and DNS changes outside Gregale.

**Cron** (`GET /v1/crons/{id}/status`, from the cron and its newest run):

| Type | `True` | `False` | `Unknown` |
|---|---|---|---|
| `Ready` | enabled and not suspended (reason `scheduled`) | disabled (reason `disabled`, terminal); suspended (reason is the suspension, such as `no_live_deployment`, which a deploy clears) | — |
| `LastRunSucceeded` | the newest run succeeded (reason `success`) | it ended otherwise (reason is its outcome: `failed`, `timeout`, `dead_letter`, `cancelled`, `uncertain`) | it is still running (reason `running`), or there is none (reason `never_run`) |

`LastRunSucceeded` describes the newest run, so it is never terminal: the
next fire changes it. To wait for one fire, `gregale cron run --wait` stays.

**Job run** (`GET /v1/jobs/{name}/runs/{id}/status`, from the run):

| Type | `True` | `False` |
|---|---|---|
| `Succeeded` | every task succeeded (reason `succeeded`) | `queued`, `running` (reason is the status); `failed`, `cancelled`, `dead_letter` (reason is the status, terminal) |
| `Progressing` | `queued` or `running` (reason is the status) | otherwise (reason `complete`, or the terminal status) |

A job run has no `Ready`: it runs to completion, so its waits default to
`Succeeded`.

### 4. The CLI: `status` and `wait` on each noun

ADR-20261026145832 adds no top-level verb, so the CLI gains standard verbs on
the existing nouns, with the same flags and exit codes everywhere:

- `gregale app status <slug> [--env SCOPE]` prints the conditions, and with
  `--json` the status document.
- `gregale app wait <slug> [--for CONDITION[=True|False]] [--env SCOPE]
  [--timeout DURATION]` polls the status every 2 s (riding out transient
  errors as `release wait` does). `--for` defaults to `Ready`, and a bare type
  means `=True`.
- **Exit codes** follow the CLI's existing scheme (`exitCodeForStatus`): 0
  when the condition holds, 1 when it is terminal and does not hold, 3 on
  timeout (the CLI's retry-later code, as `job wait` uses it), 4 when the
  resource or its conditions are not found. The final status document is
  printed with `--json`, so an agent reads the `reason` without a second
  call.
- The same verbs serve the other kinds: `gregale postgres status|wait <name>`,
  `gregale cron status|wait <id>`, and `gregale domain status <domain>` with
  `gregale domain wait <domain>` (`domain status` with no argument still lists
  every domain's certificate). Job runs already have `job status` and
  `job wait`; `job wait --for CONDITION` waits on the run's conditions and
  `job status --conditions` prints them.
- `gregale release wait` gains `--for` in a later step; `--rollout` stays as
  `--for RolledOut`. `gregale app health --wait` stays as it is: health is the
  evidence, conditions are the summary, and `app status` points to `app health`
  for the detail.

### 5. Rollout

- **Capability.** A `resource-conditions` preview capability, off by default,
  owns every status route and command (ADR-20261023115741). Customer docs and
  the dashboard come when it reaches beta.
- **Release compatibility.** The routes and types are new, so release N-1
  is unaffected. A new CLI against an apid without the route, or with the
  capability off, gets a 404 and says which existing command to use
  (`gregale app health --wait`, `gregale release wait`).
- **Steps.**
  1. This ADR.
  2. Types, mapping and the two routes, behind the capability.
  3. `app status` and `app wait`.
  4. `release status` and `release wait --for`.
  5. Domains, databases, crons and job runs.
  6. Docs, dashboard, MCP tools and a journey; then beta.

## Consequences

- An agent learns one loop for every resource it supports: act, wait for a
  condition, and read `reason` when it does not hold. `terminal` ends a wait
  that cannot succeed instead of spending its timeout.
- People get one answer to "why isn't this working?" before reaching for the
  detailed health checks or logs.
- Nothing new is stored or owned, so there is no new state to migrate, back
  up or keep consistent; a condition is only as fresh as the evidence behind
  it, which `observed_at` states.
- The 45 existing wait meanings remain until each resource moves onto
  conditions. This ADR does not deprecate them.

## Not decided here

- Labels and selectors on apps (a separate ADR).
- A server-side watch or event stream of condition changes; polling the status
  subresource is enough for the first steps.

## Amendment 1 (2026-10-31): a staged release has not rolled out

`deploy --no-traffic` leaves a release live at 0% until it is promoted. Its
`RolledOut` read `True`, "serves its full share of traffic", so
`release wait --for RolledOut` returned at once for a release production does
not serve. It now reads `False` with reason `staged`, not terminal: a later
promotion rolls it out, so a wait keeps polling until its timeout. `Ready` stays
`True`, since the release serves on its own URL.

Only the default lane (scope empty or `default`) routes by traffic weights, so
only there does 0% mean staged. A project environment, `production` included,
serves the members of its release set at 0%, and a service rollout warms its
new generation at 0% (`rollout_state` `rolling_out`) before schedd promotes it;
both keep their earlier answer. This matches the app health assessment, which
counts a live deployment as serving only above 0%.
