# How scaling to zero works

Apps on Gregale FaaS scale to zero. When nobody is using your app
its instances are parked — a snapshot on disk, zero resident RAM.
The next request wakes an instance. On the reference SSD node, Gregale targets
p95 below 350 ms for the platform interval from capacity admission/boot start
through the first upstream byte. This includes the scheduler, snapshot restore,
and Gregale's internal proxy. The full public request also includes Cloudflare,
Internet transit, client distance, and the rest of your app's response, so its
latency can be higher.

This is the trick that makes the per-GB-RAM-hour price work. The
trade-off is that the **first request to a parked app pays the
wake cost**. Subsequent requests hit the warm instance at normal
latency.

## Refresh application state after restore

If your app needs to reconnect a client or refresh an in-memory resource after
snapshot restore, configure an `after_restore` hook in `gregale.yaml`:

```yaml
lifecycle:
  after_restore:
    path: /internal/after-restore
    timeout_ms: 500
```

Gregale sends one `POST` to that path on the restored app's loopback listener
after repairing the guest clock and entropy. Return any 2xx status to allow
readiness. A timeout or other status rejects that restored instance and falls
back to a fresh cold boot. The hook runs on restore and when a
[frozen instance](#frozen-instances) resumes, not on cold boot.
The path must start with `/`; timeout defaults to 500 ms and can be at most
2 seconds. This feature applies to request and service apps.

Make the handler idempotent and require both a loopback peer address and the
`X-Faas-After-Restore: 1` request header. Gregale strips inbound `x-faas-*`
headers at the public proxy, preventing a public caller from supplying that
marker. The hook's work adds to wake latency, so keep it short. Redeploy after
changing the lifecycle configuration; existing snapshots retain the manifest
from their deployment.

A Node or Python function can declare these hooks as methods in its handler
file instead; the deploy wires them for you
([snapshot hooks](functions.md#snapshot-hooks)).

## Prepare application state before a snapshot

If your app must flush or release in-memory state before Gregale captures a
new init snapshot, add a `before_checkpoint` hook:

```yaml
lifecycle:
  before_checkpoint:
    path: /internal/before-checkpoint
    timeout_ms: 500
  after_restore:
    path: /internal/after-restore
    timeout_ms: 500
```

Gregale sends one loopback `POST` with `X-Faas-Before-Checkpoint: 1` and no
body before pausing the guest. Return 2xx to allow capture. The timeout
defaults to 500 ms and can be at most 2 seconds. The path must start with
`/`. This hook runs only for a new terminal init snapshot, not when Gregale
reuses an existing snapshot. It applies to request and service apps. Make
the handler idempotent because a failed capture may be retried, and check
both the loopback peer address and the header.

If the callback fails during the first capture, the deployment fails with
`before_checkpoint_failed`. If it fails on a later park, no new snapshot is
published and the instance stops. `gregale app wake-timeline <slug> <wake-id>`
shows `wake.park_failed` with that closed reason; `gregale logs <slug>` shows
the guest's HTTP or timeout cause. Other capture failures appear as
`snapshot_failed` in the timeline. Enabling `before_checkpoint` disables
warm-tier snapshot captures because that capture resumes the same guest
afterward.

If the callback closes connections or clears state, use `after_restore` to
reopen or rebuild them before the restored instance serves traffic. State
created during boot, such as cached credentials or IDs, is copied into every
instance restored from that snapshot. Refresh values that must be unique or
current after restore. Gregale repairs the guest clock and kernel entropy
before running `after_restore`, but cannot refresh application-held values.

### SDK helpers for the hooks

The Go, Node and Python SDKs serve both hooks for you. They answer only
loopback `POST`s with the marker header, run your callbacks in order within
1.5 s (under the 2-second limit), and run overlapping deliveries once.
Each has a helper that closes a pool's idle connections on both hooks. Those
sockets point at connections the database closed while the instance was
parked or frozen; closing them is local and fast, and the pool opens fresh
ones on demand. Gregale itself resets an app's outbound connections before it
takes a snapshot and again on a restore, so a restored instance's inherited
sockets fail at once instead of hanging, and the database frees the
snapshot's sessions immediately. A full reconnect inside `after_restore` can take longer than
the limit.

```go
hooks := faas.NewLifecycleHooks()
hooks.RecycleIdleConnections(db, 10) // *sql.DB and its usual idle size
http.ListenAndServe(":"+os.Getenv("PORT"), hooks.Middleware(mux))
```

```js
import {LifecycleHooks} from '@gregale/sdk-node/lifecycle/runtime';
const hooks = new LifecycleHooks();
hooks.onAfterRestore(() => cache.clear());
app.use(hooks.middleware()); // or: hooks.handle(req, res) in a plain http server
```

```python
from faas_sdk.lifecycle_runtime import LifecycleHooks
hooks = LifecycleHooks()
hooks.recycle_sqlalchemy(engine)  # or hooks.expire_asyncpg(pool)
app = hooks.asgi(app)             # hooks.wsgi(app) for Flask or Django
```

The helpers use `/internal/before-checkpoint` and `/internal/after-restore`;
pass other paths if your `gregale.yaml` names others. Secret rotation keeps its
own path through `SecretReloadCoordinator`
([pool runtime guide](postgres-pool-runtime.md)); do not reload secrets in a hook.

You can detect the wake tier on every routed response:

- `x-faas-wake: hot` means an already-running instance served the request.
- `x-faas-wake: restored` means the request admitted an instance from a
  usable snapshot.
- `x-faas-wake: cold` means the request admitted a fresh cold boot. The
  value is retained for CLI compatibility and is useful for retries /
  client-side banners.
- The dashboard's per-app state badge: `◌ sleeping` before
  traffic, `⟳ waking` while the instance restores,
  `● running` once it's serving. The page refreshes every 10 s.

## When an instance parks

An instance parks once it has had no requests for its idle timeout. Apps use
their plan's default (see [plans](plans.md)). Functions default to 30 seconds
on every plan, because a function's restore is cheap and most of the time
after a request is otherwise paid, unused residency. The idle reaper runs
every 10 seconds, so parking happens shortly after the timeout passes.

Set your own value from 10 seconds up to twice the plan default:

```bash
gregale app my-fn --idle 300   # keep this function warm for 5 minutes
```

### Frozen instances

On nodes where the frozen tier is enabled, an instance that reaches its idle
timeout may be frozen instead of parked: its VM stays in memory, paused, and
the next request resumes it in a few milliseconds instead of restoring a
snapshot, with its in-memory state (caches, sessions)
intact. Requests still answer `x-faas-wake: restored`. A frozen instance
holds no concurrency and is not billed; the dashboard shows it as
`◌ sleeping`. It parks after 10 minutes, or sooner when the node needs the
memory or you deploy again; on some nodes it is compressed in memory
instead, and then resumes a little more slowly. Only deployments with a snapshot are frozen, so
the first idle period after a deploy still parks. If your app declares an
`after_restore` hook, it runs when the instance resumes, as after a restore;
if it fails, the instance is discarded and the request restores from the
snapshot instead. With session affinity (`session_affinity`) on, a session
resumes the instance it was pinned to when that one is frozen.

On nodes with early freeze enabled, a `between_requests` workload (every
function by default, and any app that sets `pause: between_requests`) is
frozen a few seconds after its last request instead of at its idle timeout,
so it stops accruing usage almost as soon as it finishes serving. For the app
this is the same as a pause between requests: its timers stop, and the next
request thaws it in a few milliseconds. `when_idle` apps keep running through
their idle timeout as declared. Apps with `--min` instances are not frozen
early.

Functions and request-mode apps can be frozen; workers, services, jobs and
apps in a private network are not. Your app declares what it tolerates in
`gregale.yaml`:

```yaml
lifecycle:
  pause: when_idle   # between_requests | when_idle | never
```

| `pause` | Between requests | At the idle timeout | Default for |
|---|---|---|---|
| `between_requests` | paused after 2 s without a request; timers stop | frozen | functions |
| `when_idle` | keeps running | frozen | apps |
| `never` | keeps running | parked, as before | |

Pausing between requests and freezing are each enabled per node, like early
freeze. On a node without pausing, the instance keeps running between
requests; on a node without freezing, it parks at the idle timeout instead.

Choose `never` when your app must keep running timers or heartbeats for the
whole idle timeout and must not resume with them delayed, or `between_requests`
for a request-driven app that does no work between requests. Worker, service
and job execution modes accept only `never`, because they never idle out.

## Warm snapshots (opt-in, Pro and Scale)

By default every wake restores the snapshot Gregale takes right after your app
becomes ready, before it has served a request. Pro and Scale apps can opt in to
a second, warm snapshot, taken when an instance parks after it has served a few
requests (5 by default, `--warm-snapshot-min-requests`) and been ready for a
while (2 s, `--warm-snapshot-min-ms`). Wakes then skip your framework's warm-up
work (JIT compilation, caches filled on first use).

```sh
gregale app update shop-api --warm-snapshot
```

A warm snapshot is taken once and reused by every later wake and every instance
until the next deploy. It carries whatever those first requests left in
memory: in-memory sessions, caches, rate-limit counters and per-user data, and
files under `/tmp`, which lives in memory. Every woken instance starts from
that same state, and anything written after the capture is lost at each park.
Opt in only when your app keeps no per-user or per-request state in memory or
`/tmp`. Apps created before 2026-11-10 on Pro or Scale were opted in by
default and keep that setting; `--no-warm-snapshot` turns it off.

## Opting out: keep N instances warm (Pro, Scale)

On the Pro and Scale plans you can pin a number of instances
permanently resident via `gregale app <slug> --min N`:

```bash
gregale app my-api --min 1   # always keep 1 instance warm
gregale app my-api --min 0   # back to scale-to-zero (default)
```

Each warm instance counts as always-resident for billing — the
cost is exactly `N × ram_mb × uptime`. The bill is honest about
this; there is no premium for keeping N warm, you simply pay for
N resident GB-hours like any other plan minute.

`--min` must be in `[0, plan max_concurrency]`:

| Plan   | max concurrency | min instances allowed |
|--------|-----------------|------------------------|
| Free   | 1               | 0 (scale to zero only) |
| Hobby  | 2               | 0 (scale to zero only) |
| Pro    | 5               | 0..5                    |
| Scale  | 20              | 0..20                   |

Hobby and Free reject `--min > 0` with `403 plan_min_instances_not_allowed`.
The reaper honors the floor; RAM-pressure eviction does not (the
ceiling is physics, the floor is budget).

## Why scale to zero matters

Every parked app costs nothing to keep. A box that runs 100 apps
and serves 5 of them looks the same as a box that serves 1 of
them — most apps are idle most of the time. Scale-to-zero is
what makes the hobby plan affordable at $10/month.
