# Warm vs init snapshot: first request after a wake

What does the warm snapshot tier (ADR-055/071) buy over an init snapshot?
`TestMetalWarmVsInitFirstRequest` (pkg/fcvm, metal) cold-boots one app per
runtime and captures an **init** snapshot right after readiness, as the
deployment prime does. It then serves 50 requests and captures a **warm**
snapshot, as a Pro/Scale park did before ADR-20261110180000 made the tier
opt-in. Each snapshot is restored repeatedly, and the test times the wake
(`Manager.Wake`) and the first two requests on fresh connections. It runs
with the host page cache warm (7 restores each) and dropped before every
restore (4 each).

## Run

- Host: gregale-internal-test-1 (GCP n2, nested KVM, host clocksource tsc),
  shared acceptance lock, 2026-11-09T20:13Z.
- Revision: debc870850 (branch `bench/warm-vs-init-snapshot`).
- Stack as production: Firecracker v1.17.0 (rc.332 `firecracker-vmm`) and
  upstream jailer v1.17.0, rc.332 guest vmlinux (6.18), restore working-set
  prefetch on, `clock_realtime` on, free-page-reporting balloon on.
- Apps: Go (static, alpine), Node 22 (node:22-alpine), Python 3 (Debian),
  Java 21 (Temurin JDK, single-file source launcher), 256 MiB, 1 vCPU. Each
  serves `/work`: build 300 records, regex filter, template render, JSON
  encode, SHA-256 and gzip; Node and Python import those modules on the first
  request, as frameworks commonly do.
- Result: `--- PASS: TestMetalWarmVsInitFirstRequest (190.02s)`; leakcheck
  clean before and after. Raw samples: `samples.json`.

## Wake + first request (median)

| Runtime | Page cache | Init | Warm | Warm − init | Stored size init → warm |
|---|---|---|---|---|---|
| Go | warm | 279 ms | 276 ms | −3 ms | 66 → 71 MiB |
| Go | dropped | 605 ms | 800 ms | +195 ms | |
| Node | warm | 348 ms | 323 ms | −25 ms | 115 → 124 MiB |
| Node | dropped | 1.02 s | 0.96 s | −60 ms | |
| Python | warm | 339 ms | 322 ms | −17 ms | 90 → 90 MiB |
| Python | dropped | 840 ms | 760 ms | −80 ms | |
| Java | warm | 1.26 s | 0.42 s | **−840 ms** | 170 → 184 MiB |
| Java | dropped | 2.56 s | 2.02 s | **−540 ms** | |

First request alone (median, page cache warm): Go 43.8 → 37.2 ms, Node 33.5 →
33.8 ms, Python 67.1 → 59.4 ms, Java 1,026 → 174 ms. The second request is
steady state in every case (5–30 ms).

## Reading

- For Go, Node and Python the warm tier changes the first request by 0–80 ms,
  inside the spread between restores; Go was slower from a warm snapshot with
  a dropped cache. An init snapshot already holds what these runtimes load.
- Java's first request after an init restore pays about 1 s of class loading
  and JIT compilation; a warm snapshot removes 0.5–0.8 s of it. Other JIT
  runtimes (.NET) and large framework apps likely behave the same way.
- Warm snapshots are 0–14 MiB larger on disk.
- The warm snapshot here was primed by 50 synthetic requests, not customer
  traffic, and still captured the whole Java gain. Priming the init snapshot
  with synthetic requests would keep that gain without capturing request
  state (sessions, caches), which is why ADR-20261110180000 made the
  traffic-primed warm tier opt-in.

## What it does not cover

- Absolute wake times on this nested-virtualisation host are not production
  numbers; compare tiers, not hosts.
- The probe apps are small. Real framework apps (Next.js, Django, Spring) have
  larger first-request costs, so they gain more from either kind of priming.
- Run it again with `FAAS_RUN_WARM_INIT_BENCH=1`; see the test's doc comment.
