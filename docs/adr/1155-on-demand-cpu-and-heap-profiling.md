# ADR-1155: On-demand CPU and heap profiling

- **Status:** accepted for internal implementation; native acceptance pending
- **Date:** 2026-10-10
- **Decision:** Ship dormant guest collectors that the control plane can arm
  for a bounded CPU and/or heap capture on one running instance without a
  redeploy. apid queues the capture in Postgres, schedd claims it and asks the
  owning vmmd, vmmd delivers the request on guest-init's control listener and
  returns the collectors' unparsed pprof profiles, and apid parses, merges and
  renders them. Continuous profiling (ADR-819) also gains heap profiles.
- **Why:** Continuous CPU profiles answer "what got slower across deploys";
  they cannot answer "what is this instance doing right now" or "where is this
  memory leak" without a redeploy that changes the behavior being
  investigated. CPU-only collection also leaves memory growth unexplained.

## Context

ADR-819 collects CPU only, and only for deployments whose manifest sets
`profiling.enabled`. Enabling it disables warm snapshots and init-snapshot
reuse (the checkpoint handshake must drain running profilers), so it cannot be
the default. Customers need to profile a misbehaving instance as it is.

## Decision

### Dormant collectors

imaged sets `profiling_on_demand` in a deployment's guest manifest when the
operator enables `FAAS_PROFILING_ON_DEMAND=1` and the account's plan includes
profiling. guest-init then starts the local bridge (127.0.0.1:9191) with
`enabled=false` and stamps the managed Node and Python preloads, exactly as
continuous profiling does. Go apps that call `guestprofiling.Start` are
covered too.

A dormant collector loads no profiler and polls the bridge once per second;
an armed capture starts within one poll. Because nothing runs, a dormant
bridge answers a checkpoint immediately, and on-demand alone does **not**
enable the before_checkpoint handshake or disable warm/reused snapshots.

### Capture flow

1. `POST /v1/apps/{slug}/profiles/captures` inserts a `queued` row in
   `profile_captures` (one active capture per app, 60 per account per hour)
   and emits `pg_notify('profile_capture')`. apid never calls schedd or vmmd.
2. schedd's drain claims queued rows with `FOR UPDATE SKIP LOCKED`, runs each
   capture in its own goroutine (at most four per schedd) and picks the
   requested or earliest-started RUNNING instance. Parked apps are not woken:
   a fresh process would not show the state under investigation.
3. vmmd's `CaptureProfile` RPC holds the instance's in-flight activity, so
   the idle reaper does not park it mid-window, and sends message type 7 on
   guest-init's control listener (vsock 1024).
4. guest-init switches the bridge to a fresh capture epoch with the requested
   kinds. Collectors restart, profile for the window, and stop and flush when
   the bridge disables the epoch at its end. After a 3 s grace the reply
   carries up to 8 profiles (3 MiB total). Continuous configuration then
   resumes under a new epoch; continuous CPU uploads in a capture epoch are
   forwarded as usual, so continuous coverage has no gap.
5. schedd stores the profiles unparsed in `profile_capture_data` and marks the
   capture `ready`, or `failed` with a customer-facing reason. Captures stuck
   `capturing` after their window plus slack, or `queued` for 10 minutes, are
   failed; all captures expire after 7 days.
6. apid parses, normalizes (CPU nanoseconds or live heap bytes; customer
   labels dropped) and merges a kind across processes for the
   `/view` (function table and call tree) and `/pprof` (download) endpoints.

Only apid parses pprof. vmmd bounds and relays the JSON envelope; schedd
stores bytes.

### Heap semantics

| Runtime | Collector | A capture reports |
|---|---|---|
| Go | `runtime/pprof` heap profile | the sampled live heap of the process |
| Node | V8 sampling heap profiler (`@datadog/pprof`, already pinned by the Pyroscope SDK) | allocations made during the window that are still live at its end |
| Python | `tracemalloc` snapshot encoded as pprof | allocations made during the window that are still live at its end |

"Allocated during the window and still live" is the signature of a leak.
Node `Buffer` contents live outside the V8 heap and appear as `(external)`.
`tracemalloc` costs Python throughput while tracing, so it runs only while a
heap capture or continuous heap collection is active.

### Automatic heap capture on memory pressure

Leaks are rarely caught by hand before the OOM kill. While on-demand
profiling is enabled for the deployment, guest-init samples `/proc/meminfo`
every 2 s. When `MemAvailable` falls to 15% of `MemTotal` and a profile
collector has polled the bridge in the last 2 s, it sends one
memory-pressure report (guest event type `0x0A`: available, total and
anonymous MiB) and re-arms only after availability recovers to 30%.
`MemAvailable` counts reclaimable cache, so file-heavy workloads do not
trigger, and it reflects the guest's real headroom regardless of kernel
overhead.

vmmd relays the report best-effort (no outbox: a lost report only means no
automatic capture) to schedd's `ReportMemoryPressure` RPC. schedd queues a
10 s heap capture on that instance with `trigger: memory_pressure` and a
`trigger_detail`, under the same quota and single-active rules as manual
captures plus at most one automatic capture per app per hour, and wakes its
capture drain. Reports for parked instances, plans without profiling and
instances owned by another scheduler are acknowledged and skipped.

### Reading captures

Capture views carry the same commit-pinned source links as continuous
profiles: frames are linked to the capture deployment's recorded GitHub
commit, with Go module and standard-library paths outside the repository
left unlinked. The dashboard capture page renders a zoomable, searchable
flamegraph per kind, the CLI prints source URLs, and continuous heap views
are linked the same way.

### Continuous heap profiles

`profiling.kinds: [cpu, heap]` adds heap snapshots once per window to
continuous collection. profiled stores them as the Pyroscope series
`memory:inuse_space:bytes:space:bytes` without route frames or coverage
metadata. `GET /v1/apps/{slug}/profiles/heap` narrows the query to the last
collection window before `end`, since merged snapshots add up.

## Consequences

- Existing deployments gain dormant collectors on their next deploy after
  the operator flag is set.
- A capture briefly runs profilers in a serving instance; CPU sampling costs
  match ADR-819, heap sampling costs are runtime-specific as above.
- A capture interrupted by a park, migration or schedd restart fails with a
  reason rather than returning partial data.
- Custom images still need an instrumented runtime; their captures finish
  `ready` with zero processes and an explanatory reason.

## Amendment: captures that outlive an OOM kill, and capture comparison

**OOM kills.** The automatic memory-pressure capture exists to show the heap
before an OOM kill, but a fast leak reaches the kill inside the 10 s window,
and schedd destroys the instance as soon as guest-init reports it. Three
changes keep that heap:

1. schedd asks automatic heap captures for checkpoints
   (`checkpoint_ms` = `ProfileCaptureCheckpointInterval`, 2 s). Collectors
   post interim heap profiles with `?checkpoint=1`; the bridge keeps the
   latest per process within the reply's byte budget, so a guest already
   short on memory holds at most one reply's worth.
2. guest-init's OOM listener interrupts a running capture before it emits the
   workload-OOM event and waits until the reply is written to the host,
   bounded by `ProfileCaptureOOMFlushTimeout` (3 s). Processes without a
   final profile contribute their last checkpoint; the reply is marked
   `partial`.
3. schedd records the instance on the capture row when it starts it, and its
   workload-OOM path merges `oom_killed_at`, `oom_peak_mb` and `oom_plan_mb`
   into the newest heap capture started on that instance within
   `ProfileCaptureOOMLinkWindow` (15 min). The fields live in the capture
   document, so no migration is needed; re-stamping on a redelivered OOM
   report is idempotent.
4. Single-process apps run without a workload cgroup leaf, so ADR-121's
   memory.events watcher never sees their OOM kills; the supervisor restarts
   them in place and no workload-OOM report is sent. guest-init therefore
   also treats an app exit by SIGKILL while the kernel's `/proc/vmstat`
   `oom_kill` counter rose as an OOM kill: it interrupts a running capture
   and marks the reply `oom_killed` with the guest's memory as the peak, and
   schedd stamps that capture with the instance's plan memory. This adds no
   lifecycle change: whether such kills should fail the deployment like
   workload OOMs is an ADR-121 decision and is left as it is.

Checkpoints are limited to automatic captures: they cost a heap snapshot per
interval (noticeable for Python's `tracemalloc`) and manual captures of a
healthy instance do not need them.

**Comparison.** `GET /v1/apps/{slug}/profiles/captures/{id}/diff?base=&kind=`
merges both captures of one kind and returns every call path and function
with both values and the difference. Named frames pair by name and file so
moved lines do not split a path. Values are not rescaled: Node and Python
heap values cover each window, and Go's are the process's live heap, so no
single normalization is right for all runtimes; differing durations and
deployments are reported as warnings instead. The dashboard renders the
difference as a flamegraph whose width is the sum of both captures, which
keeps children inside their parents.

## Amendment: stack snapshots and slow-request captures

CPU and heap captures do not explain a request that hangs while the process
is idle. Captures gain a `stacks` kind, an instant snapshot taken when the
capture starts (captures only; continuous `profiling.kinds` stays cpu/heap):

- Go uploads the goroutine profile; Python encodes `sys._current_frames()`
  plus the stacks of pending asyncio tasks, found through each thread's
  `run_forever` frame and `asyncio.all_tasks(loop)`; Node reports the event
  loop's active handles and requests by type, because a JavaScript thread
  cannot sample its own stack while running the collector.
- Profiles normalize to `threads`/`count` and reuse merge, view, comparison
  and storage (`profile_capture_data.kind` admits `stacks`). A capture holds
  up to three kinds (`ProfileCaptureMaxKinds`).

gatewayd-internal tracks proxied requests (not streaming responses or
upgrades) for apps whose plan includes profiling, when
`FAAS_PROFILING_ON_DEMAND=1`. A request in flight for
`ProfileCaptureSlowRequestAfter` (10 s) is reported, at most once per app per
minute, through the new schedd RPC `ReportSlowRequest` to the schedd that owns
the instance. schedd queues a stacks+CPU capture of 5 s with trigger
`slow_request`, at most once per app per `ProfileCaptureSlowRequestMinInterval`
(15 min), recording the request id and trace id. The request explorer looks
captures up by trace id and links them (`profile_capture_id`). The gateway
reports only route labels, never raw paths.

## Amendment: announcing automatic captures

Automatic captures were only visible to someone already looking at the
profiles page. A capture is now *announced* when it is ready with at least
one profile and either the platform started it (memory pressure, slow
request) or an OOM kill is linked to it (`ProfileCapture.Announced`). Manual
captures without a kill are not announced; whoever asked is already waiting.

- Two app webhook events, delivered through the existing app webhook outbox
  with its signing, retries and event filters. A subscription with no filter
  receives them, like `profile.route_regressed`:
  - `profile.capture_ready`: enqueued in the transaction that finishes an
    announced capture.
  - `profile.capture_oom_linked`: enqueued in the transaction that links the
    first OOM kill to a capture that was already ready. A kill linked while
    the capture is still running is announced by `profile.capture_ready`,
    so each fact is delivered once. schedd links a kill reported in the
    capture reply before finishing the capture for the same reason.
  The outbox deduplicates by event and capture id. The payload
  (`ProfileCaptureWebhookPayload`, version 1) is the capture record plus its
  dashboard and API paths, never profile samples.
- The app page shows a banner while announced captures are retained: how
  many in the retention window, how many an OOM kill followed, and a link to
  the newest.

schedd already writes the outbox for `app.woken`; no new writer or table is
introduced. Volume stays bounded by the capture quotas:
one automatic capture per app and trigger per minimum interval, 60 captures
per account per hour.

## Amendment: automatic CPU capture on CPU saturation

Memory and latency incidents leave a capture; a runaway loop or a traffic
spike that holds an instance at its CPU limit did not. schedd's instance
stats poller already receives each instance's host cgroup CPU use from
vmmd, and schedd's admission ledger holds each instance's sustained CPU quota
(`cpu_millicores`), so the check reads neither the store nor the guest:

- An instance is saturated while its CPU use is at least
  `ProfileCPUSaturationPercent` (90%) of its sustained quota. A startup
  boost makes the quota unknown, so boosted instances never count. A dip
  shorter than `ProfileCPUSaturationGrace` (5 s) keeps the episode; a
  longer dip, an unknown quota or the instance leaving the snapshot ends it.
- An episode lasting `ProfileCPUSaturationAfter` (30 s) calls schedd once,
  which queues a CPU capture of `ProfileCaptureCPUSaturationDuration`
  (10 s) with trigger `cpu_saturation`, at most once per app per
  `ProfileCaptureCPUSaturationMinInterval` (15 min), under the same account
  quota and single-active rules as every capture. The capture is announced
  like the other automatic captures.
- schedd enables this only with `FAAS_PROFILING_ON_DEMAND=1`.

The quota is the one admitted with the instance: a live CPU-limit change
applies to instances started afterwards. On a multi-node control plane, an
instance on a node whose schedd did not admit it has no quota there and is
not checked.

## Amendment: collecting from net/http/pprof

Captures of a Go image that never calls `guestprofiling.Start` finished
`ready` with zero processes, although many Go services already serve
`net/http/pprof`. `profiling.pprof_port` names that guest-local port; guest-init
then collects for the workload:

- A collector inside guest-init, started with each run of the workload and
  labelled with its PID, follows the bridge's control exactly as the Go SDK
  does: CPU from `/debug/pprof/profile?seconds=<window>`, heap from
  `/debug/pprof/heap` at the window end and at each checkpoint, stacks from
  `/debug/pprof/goroutine` when a capture starts. Continuous collection works
  the same way, without route labels. It reads loopback only; nothing is
  exposed beyond the VM and no host component changes, so the profiles enter
  the existing bounds, storage and parsing.
- It registers with the bridge only after `/debug/pprof/` answers, so an
  endpoint that never comes up neither counts as a process nor enables
  memory-pressure reports; a capture then says so in its reason. A kind the
  endpoint fails to produce is named in the reason too, by status only, never
  the response body.
- It stands down while a collector of the application's own polls the
  bridge: Go allows one CPU profiler per process, and two heap collectors
  would double-count. It acknowledges checkpoints like a collector, abandoning
  an in-flight window, so continuous profiling keeps its snapshot handshake.
- Ports 9191 (profile bridge) and 4318 (trace bridge) are refused; the
  application's own port is allowed, since the handlers often share its mux.

`pprof_port` is an `omitempty` field of the existing profiling configuration,
stored in the app manifest JSON: no migration, and older guests ignore it.

## Amendment: Go lock contention (block and mutex)

A Go service that is slow while mostly idle waits on locks or channels, which
neither CPU nor stack captures attribute. Captures gain Go's `block` and
`mutex` kinds (captures only; a capture may now request all five kinds):

- Both are read as the difference over the window, never the process's
  cumulative history. The Go SDK takes a baseline profile when the window
  opens and uploads the difference when it closes. The pprof-endpoint
  collector requests `/debug/pprof/{block,mutex}?seconds=<window>`, which
  `net/http/pprof` answers with the same difference.
- The SDK samples only for the window (`ProfileBlockRateNanos`,
  `ProfileMutexFraction`) and restores what it changed. A mutex fraction the
  application set is read and kept. Go has no getter for the block rate, so
  the first capture decides ownership: existing block samples mean the
  application enabled it, and captures then never change the rate. The
  pprof-endpoint collector never changes sampling; without the application's
  own rates those profiles are empty.
- apid normalizes both to `delay` nanoseconds, dropping samples that cancelled
  to zero in the difference; views, downloads and comparison work as for
  other kinds, and comparison's default kind falls back to mutex, then block.
- `profile_capture_data.kind` admits the two kinds. The table is new on this
  branch, so its CHECK is extended in place rather than by a second migration.
- A guest built before these kinds rejects such a request as invalid. vmmd
  now reports that refusal as its own error, and the capture fails with a
  reason that says to redeploy instead of claiming the guest has no
  collectors.

## Rejected alternatives

- **apid → schedd gRPC.** Violates the ownership rule (apid records intent in
  Postgres); the queue also survives restarts.
- **Waking parked apps to profile them.** Profiles a different process than
  the one under investigation and costs a wake per click.
- **Parsing in vmmd or guest-init.** vmmd is the only root component; guest
  merging would pull pprof into PID 1 for no benefit.
- **Full heap snapshots (`.heapsnapshot`).** Size scales with the heap and
  pauses the process; deferred until object-storage transfer exists for
  multi-megabyte guest artifacts.
