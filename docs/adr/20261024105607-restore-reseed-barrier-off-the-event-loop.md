# ADR-20261024105607 · The restore reseed barrier is answered off the event loop

- **Status:** Proposed
- **Date:** 2026-10-24
- **Amends:** [ADR-680](680-restore-userspace-rng-reseed.md) (how a Node process
  answers the barrier), [ADR-1110](1110-node-rng-caches-drained-on-first-use.md)
  (adoption on first use now covers `Math.random` too)
- **Switch:** vmmd `restore_reseed_thread` / `FAAS_RESTORE_RESEED_THREAD`,
  default off

## Context

ADR-680's barrier holds the resume ACK until every registered Node or Python
process confirms its userspace reseed. A Node process answers on its event
loop: the reseed line wakes libuv, runs the preload's JavaScript, calls the
addon's `RAND_poll()`, reseeds the `Math.random` replacement and writes
`ok`. On a restored guest each page that path touches is an EPT fault.

Production-us on rc.290 (read-only bpftrace on gregale-compute-1/-2,
2026-10-24, 10 restores of four Node apps):

- `resume_hook` p50 118–150 ms, with ~5,200 guest page faults inside it. The
  bench guest (busybox app, no barrier client) takes ~2,560 with the same
  kernel and guest-init.
- The extra ~2,700 are the app process waking: ~1,950 in Node and its
  libraries, ~280 in the 0x400000 range, ~450 in the guest kernel serving
  them. They start ~60 ms into the hook and peak at 80–140 ms, inside
  `reseedRestoredWorkloads`.
- The rc.290 guest timing frame showed the barrier at ~55 ms, the largest
  guest stage.

Production is moving to bare metal, where a fault costs ~1–3 µs instead of
~11–13 µs, so the fix has to remove work from the critical path rather than
make faults cheaper.

### Option 1: acknowledge first, hold requests until the reseed lands

vmmd would get the ACK after the kernel reseed and probe readiness while the
app's processes reseed. That moves the barrier only if something holds the
first request until the reseed completes:

- Readiness does not hold it. After a restore the app's listener already
  exists, so vmmd's TCP probe is answered by the guest kernel without the app
  running, and an HTTP or gRPC probe would itself be served by the
  unreseeded app.
- guest-init cannot hold it. `vmmd-stream-bridge` dials `10.0.0.2:<port>`
  straight into the app's listening socket (ADR-009 networking); there is no
  guest-init hop on the request path.
- The app cannot order it. The reseed line and the first connection can
  become readable in one epoll pass, and libuv may run the request first.

Holding requests would need new data-path machinery in every guest (packet
queueing or a guest-init proxy in front of each app port), and readiness
would stop implying "reseeded". The `wake.restore_breakdown` phases would
move cost from `resume_hook` into `wait_ready` and the gateway's
`proxy_first_byte` without shortening either.

### Option 2: answer without the event loop

Of what the event-loop answer does, only the OpenSSL reseed must finish
before serving. `Math.random` and Node's RNG caches are JavaScript state that
ADR-1110 already adopts lazily: a restore marks the caches stale and the first
call that reads one drains it.

## Decision

With the switch on, vmmd adds `faas.reseed=thread`
(`api.RestoreReseedThreadBootArg`) to an app VM's cold boot. guest-init stamps
`GREGALE_RESEED_MODE=thread` into the workload env with the preloads.

1. **Node: a native barrier client.** The preload calls the addon's
   `start(socket)`. The addon connects, says `hello node-thread <pid>`
   synchronously, and starts one thread per process with signals blocked.
   On `reseed <nonce>` that thread calls `RAND_poll()`, increments a
   process-wide restore generation, and answers `ok`. It never enters
   JavaScript or libuv. It touches its stack, the addon and OpenSSL's reseed
   path.
2. **Node: adopt the restore before the next value.** `Math.random`, the
   `randomUUID`/`randomInt` wrappers and webcrypto's `randomUUID` compare the
   generation with the one they last adopted (`Atomics.load` on an external
   ArrayBuffer over the addon's static word). On a change they reseed the
   xoshiro state from `crypto.randomBytes(16)`, which the reseeded OpenSSL
   serves, and mark both caches stale (ADR-1110's drain still runs after
   `RAND_poll()`). After answering, the thread writes one byte to a pipe the
   preload reads, so an idle event loop adopts the restore at once, alongside
   readiness and the platform's publication work instead of in the ACK path.
3. **Node worker threads.** The preload runs in each worker isolate. In
   thread mode a worker does not register. The process's one thread answers
   for it, and the worker adopts each restore from the same generation on
   first use. If the main thread's client could not start, a worker
   registers through the event-loop client as before.
4. **Python is unchanged.** Its client already runs on its own thread, and
   its reply must run Python code to reseed `random`. A minimal reply
   (raw `recv`, an integer seed, `_ssl.RAND_add`, no `import ssl`) was
   measured and dropped: it showed no reliable gain (see Consequences).
5. **Unchanged:** the socket protocol, the 2 s budget, fail-closed NACK 15,
   the capability byte, the opt-out and the coverage list. A thread-mode
   client that gets a malformed nonce answers `err bad_nonce`. A
   failing `RAND_poll()` answers `err openssl_reseed_failed`. Without the
   addon, the preload falls back to the event-loop client, which fails closed
   as before.

### Guarantee

Before the resume ACK, every registered process has reseeded OpenSSL's
primary DRBG. Every per-thread DRBG reseeds from it before its next output
(OpenSSL 3 reseed propagation, which ADR-680 already relies on). Each
JavaScript generator either has been reseeded or reseeds before it returns its
next value. ADR-1110 accepted this "before the next value" form for Node's
caches. This ADR extends it to `Math.random`. Invariant 5 holds: two restores
of one snapshot draw different nonces and kernel state, so they never share
an RNG stream.

A synchronous JavaScript loop that runs across the capture finishes its
current `Math.random` call on the old state. That is no worse than ADR-680,
whose event-loop answer could not run until that loop yielded. In thread
mode, OpenSSL is reseeded even while such a loop runs.

## Consequences

Measured with `TestMetalRestoreReseedTimingBench` on gregale-internal-test-1
(GCE n2, nested KVM, 2026-10-24): Firecracker 1.17 and guest kernel 6.18 from
production, guests with 2 vCPUs and 1024 MiB like a Pro instance, and the
repo-pinned `node:22-alpine` runner base. Each cell is the p50 / p90 of 10
restores of one snapshot. The host's load average was 7–10 on 2 vCPUs from
other sessions' builds, so wall times are noisy: the reseed-off arm's
`resume_hook` p50 alone moved between 123 and 226 ms across runs. The guest's
own barrier stage, `guest_reseed_slowest_us`, varies least.

| app, request delay | arm | barrier stage | resume_hook | first request |
|---|---|---|---|---|
| node, 0 ms | off | — | 130 / 326 | 120 / 156 |
| | event loop | 84 / 103 | 262 / 394 | 76 / 99 |
| | thread | 22 / 25 | 194 / 281 | 98 / 144 |
| node-plain, 0 ms | off | — | 226 / 291 | 102 / 117 |
| | event loop | 85 / 127 | 277 / 526 | 46 / 70 |
| | thread | 23 / 33 | 219 / 324 | 100 / 220 |
| node, 100 ms | off | — | 133 / 282 | 39 / 66 |
| | event loop | 87 / 170 | 308 / 433 | 40 / 51 |
| | thread | 22 / 30 | 182 / 205 | 36 / 68 |
| node-plain, 100 ms | off | — | 123 / 357 | 25 / 29 |
| | event loop | 79 / 119 | 244 / 290 | 20 / 24 |
| | thread | 16 / 25 | 161 / 253 | 23 / 36 |

`node` calls `randomUUID`, `randomInt`, `randomBytes` and `Math.random` on
every request; `node-plain` calls none. The request delay stands in for the
platform's work between readiness and the first request (vmmd's reply,
schedd publication, gateway routing), which takes ~60–150 ms in production.

- The barrier stage drops from ~80–90 ms to ~16–23 ms p50 (−72 to −80%).
  `resume_hook` drops with it, by less than the noise allows to state
  precisely.
- The event loop's wake does not go away; it moves. With no delay the bench
  sends its request as soon as the wake returns, before the wake pipe has
  run, and the first request pays the wake: thread-mode first requests
  (98 and 100 ms) look like the barrier-off ones (120 and 102), not the
  event-loop ones (76 and 46). With a 100 ms delay the wake pipe finishes
  first, and thread mode's first request matches the event-loop client's
  (36 vs 40 and 23 vs 20 ms). In production the platform interval plays the
  delay's part, so the barrier's share of the critical path is what is
  saved.
- First byte (wake plus first request) is too noisy on this host to
  confirm the saving end to end: p50 567 → 555 and 704 → 492 ms without
  the delay, 637 → 433 and 474 → 484 ms with it.
- EPT faults inside `resume_hook` with guest RIP in the node executable
  (per-restore median): 26–68 in thread mode against 94–1,562 for the
  event-loop client and 0 with the barrier off. One thread-mode cell
  (node-plain, no delay) had 569, when the wake pipe's event-loop work
  overlapped the end of the hook. The native client's ~60–100 user-space
  faults cost ~1–2 ms at nested-fault prices. The rest of the ~16–23 ms is
  guest kernel work (thread wakeup, the socket round trip, `getrandom`) and
  vCPU scheduling on a loaded host, which this bench cannot separate.
- Python is left as it is. On the same host, with `python:3.12-slim`, 2 vCPUs
  and no request delay, the barrier stage was 52 / 72 ms (p50 / p90) for
  today's listener thread and 44 / 85 ms for the minimal reply, with
  `resume_hook` 200 / 326 against 238 / 351 ms. Python's cost is therefore
  not in the reply's few lines but in the interpreter waking. Taking it
  off the critical path would need a native extension per Python ABI, or
  lazy adoption wrappers around `random`. Each needs its own ADR.
- The barrier stops waiting for event loops: Node's share of `resume_hook`
  becomes the thread's wake plus `RAND_poll()`. Worker isolates no longer join
  the barrier.
- The event loop's wake still faults the same pages. It now happens in
  parallel with vmmd readiness and the platform's publication. The first
  request pays only for whatever had not finished when it arrives.
- Every thread-mode Node process has one extra thread (256 KiB stack
  reservation, a few pages resident) and one pipe.
- The switch changes only cold boots and the snapshots they capture. A
  restored guest keeps the mode it was captured with. Both modes are safe, so
  turning the switch off needs no snapshot retirement.
- `make rng-addon-check` still proves `reseed.node` is the reproducible build
  of `reseed.c`. The ELF stays libc-independent. Its new imports
  (`pthread_*`, `socket`, `connect`, `pipe`, `fcntl`, `read`, `write`,
  `close`, `getpid`, `memcpy`) resolve against the node process's libc under
  glibc and musl.

## Verification

- Runtime (real node + addon, guest/init): thread mode answers the barrier
  while the event loop is blocked. `Math.random` adopts the restore inside
  the first call after it. The wake pipe adopts it on an idle loop. A worker
  adopts on first use and does not register. A short nonce is refused. With
  no addon the preload fails closed. A Python process booted in thread mode
  keeps its client, reseeds the parent and forked children, and refuses a
  short nonce. These ran in `node:22-alpine` (musl), `node:24-bookworm-slim`
  (glibc 2.36) and `python:3.12-slim`.
- Metal: `TestMetalRestoreUserspaceRNGDiffers` gains `reseed-thread` arms
  for Node and Python. All six arms passed: in thread mode Node's uuid,
  randomInt, randomBytes and Math.random and Python's random and ssl differ
  across two restores of one snapshot, and both reseed-off controls replay. `TestMetalRestoreReseedTimingBench` gains the
  `reseed-thread` arm, a request delay for the platform interval, and vCPU
  selection.
- Rollout gate: enable on one compute node, compare `resume_hook`,
  `guest_userspace_reseed_us` and first-byte against its peers for a day,
  then the fleet.
