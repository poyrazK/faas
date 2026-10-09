# Application health

Read the current assessment without waking your app:

```sh
gregale app my-api health
gregale --json app my-api health
```

The equivalent API is `GET /v1/apps/{slug}/health`, authorized by `apps:read` or
`admin`. App ownership is checked before collecting evidence. Structural
checks are available on every plan; request telemetry follows the existing
Hobby+ metrics entitlement.

| Status | Meaning |
| --- | --- |
| `healthy` | Available, current evidence shows no health issues. |
| `degraded` | A warning needs attention, such as a partial replica deficit, an elevated 5xx rate or a failed latest release while an older release serves. |
| `unhealthy` | Known evidence shows a serving failure, such as no ready required replicas or all observed serving replicas unready. |
| `unknown` | Missing, stale, unavailable or truncated evidence prevents confirmation. |

Each response includes `checks` with a stable `code`, status, explanation,
optional diagnostic `reason` and an inspection action. Readiness checks include
independent `findings`, identifying the required source, affected deployment and
replica. Recorded readiness transitions and last node heartbeats are labelled
separately. Missing evidence has no invented observation time. Up to 64 findings
are returned with known failures first and stable target order within severity; `findings_truncated` reports omitted details
without reducing the capacity counts. Lifecycle phases such as `idle`, `deploying`
and `stopped` are separate from confidence in health. The customer app overview
links checks to existing release, log, error, metrics and configuration views.

Readiness combines running replica state, the independent required primary-app
and primary-ingress companion probes, and current node heartbeat evidence.
Optional companion probe failures do not gate the main route. The warm-snapshot
framework-ready timestamp is not used as a serving health signal. A service's
zero replica target is intentional; a request workload with no warm target and
cold-boot artifacts can be normally idle. The read never tests a cold wake.

Request evidence covers the last **5 minutes** for the **current traffic-bearing
default-scope releases**. Previous releases and other environments are excluded.
Only 5xx responses count as failures; 4xx responses remain in the denominator.
The `requests` object identifies assessed deployment IDs, coverage, counts, rate
and the diagnostic policy. Counts are confirmed only when `requests.known` is
true; zero placeholders in an unconfirmed response are not measured successes.

| Observed request evidence | Request check |
| --- | --- |
| No observed requests | Informational: request success has not been exercised. |
| Successful samples with no 5xx | Pass for the available sample. |
| Errors with fewer than 50 requests | Unknown severity; counts remain visible. |
| At least 50 requests and 5 errors, with at least 5% 5xx | Warning. |
| At least 50 requests and 5 errors, with at least 25% 5xx | Failure. |
| Errors below the warning thresholds | Pass; error counts and investigation remain visible. |

These are diagnostic defaults, independent of your SLO. One 5xx among 10,000
requests remains visible but does not degrade overall health. Ten errors among
ten requests are unconfirmed severity, not a healthy zero.

An actual telemetry timestamp is required for every selected release; the
oldest release's latest sample must be within **2 minutes**. Each release needs
samples sufficient to calculate a counter increase. Every observed status-class
counter needs at least two samples; a newly introduced 5xx counter is incomplete
evidence rather than a healthy zero. Missing or stale samples,
failed reads, invalid values and incomplete deployment coverage are unknown.
Requests recorded without a deployment, including routing failures or telemetry
label overflow, cannot be assigned to an environment and prevent confirmation.
App-wide metrics are never substituted for incomplete scoped evidence.

Assessment timestamps expire after **120 seconds**. Clients should refresh
rather than continue displaying a healthy cached result. The console refreshes
while visible every 30 seconds and hides expired evidence. API read failures
and unreachability take precedence over cached success.

This first slice assesses default-scope HTTP request and service workloads.
It excludes preview/dark releases, mirrors and one-off tasks from serving
capacity. Worker/job execution is explicitly unassessed. Active-instance scans
are bounded to 256 rows, deployment history to 50 recent rows, and collection
to 10 seconds; incomplete evidence is reported instead of inferred as zero.
`capacity.known` distinguishes a measured count from unavailable evidence.
The existing live-release read supplies serving revisions independently of the
bounded history window, so a failed recent release does not hide an older live
release. Raw deployment errors, probe output and infrastructure addresses are
not exposed.

This assessment does not prove public reachability, verify artifact existence,
probe dependencies, or provide an uptime guarantee. It describes evidence that
Gregale already has. Opt-in notifications for status changes are described
below. Active public probes, arbitrary environment selection and worker/job
checks are future slices.

## Recorded health history

Read retained background observations:

```sh
gregale app my-api health --history
gregale app my-api health --history --limit 20 --before ENTRY_UUID
gregale --json app my-api health --history
```

The API is `GET /v1/apps/{slug}/health/history`, with the same read scope and
ownership checks as current health. History remains readable after a plan
downgrade. Collection uses each account's current request-metrics entitlement.

A background collector assesses eligible HTTP request and service apps without
requiring an open dashboard. The target spacing is at least 30 seconds per app;
fleet load and failures can delay it. It reuses recorded state and telemetry and
never wakes or probes the app. Each app has a fenced lease, so an expired worker
cannot overwrite a newer observation. API reads do not create or refresh history.

The timeline records a first assessment and meaningful changes to status,
phase, releases, diagnostic reasons, replica findings or severity policy.
Changing counters and timestamps alone do not add events. Entries contain the
original assessment and policy; the separate `latest` field contains the newest
background assessment even when no event was added. `collector_fresh` indicates
only that this background assessment has not expired, independently of health.
Clients also expire cached collection freshness using `latest.evaluated_at` and
`latest.valid_for_seconds`.

After a collection gap longer than the prior assessment's validity, the next
successful collection records an unknown `gap` entry at the old evidence's
expiry and then the new observation. Until collection resumes, freshness is
false and the last historical entry retains its original meaning and time.
Unknown is not a confirmed outage or a confirmed recovery.

History is newest first, with up to 100 entries within 4 MiB per app, each at
most 64 KiB. Entries older than 30 days are excluded from reads and removed by
bounded background cleanup. Pages default to 20 entries, with a maximum of
100. `next_cursor` supplies `before` for the next page. An unavailable, foreign,
aged or pruned cursor returns 404; refresh from the first page. History begins
with collection after installation; older incidents are not backfilled.

The console shows recorded release and replica links. These resources may have
been retired since the observation. Times describe assessments or evidence
expiry, not exact incident start/end, uninterrupted recovery or deployment
causality. History does not establish current public reachability or uptime.

Operators can inspect `app_health_collection_total{outcome="recorded"|"error"}`
and `app_health_collection_duration_seconds`. A recorded unknown assessment
counts as a recorded observation, never as healthy. Lease, batch, deadline,
interval and retention bounds are centralized in `pkg/api/limits.go`.

## Health-change notifications

Subscribe an app webhook explicitly to `app.health.changed` in the console or
with the existing `gregale webhooks add --app APP --target-url HTTPS_URL --event
app.health.changed` command and its signing-secret options. Empty event filters
cover standard platform events and do not opt in to health notifications.
Existing webhook plan availability, signing, retries, replay and quotas apply.

The first background observation establishes a quiet baseline. Later status
changes notify; moving counts, phase changes and replica findings without a
status change do not. Known statuses use `worsened` or `improved`; changes into
or out of `unknown` use `unconfirmed` or `confirmed`, never an inferred outage
or recovery. An evidence gap resets the comparison and discards pending changes.
Notifications describe default-scope HTTP assessments, not public reachability.

A five-minute cooldown combines pending changes into the latest observed
status. Returning to the last announced status cancels the pending notification.
The next successful collection after cooldown can send it, so collection load
or failure can delay notification. Recipients are captured at the change; later
subscriptions cannot receive that old change, and reconfiguring a subscription
before a deferred event commits removes it from the pending recipient snapshot.
No incidents are backfilled.

`AppHealthChangedWebhookPayload` contains the comparison status, current sampled
status, transition ID and time, fresh assessment and queue times, coalescing
flag, release IDs and an authenticated history path. With coalescing, the
comparison can span multiple observations; `transition_id` identifies the last
status-change entry while `evaluated_at` identifies the fresh assessment used
to queue it. Retention can remove linked evidence. Deduplicate repeated HTTP
attempts by the stable delivery ID; delivery is at least once.
