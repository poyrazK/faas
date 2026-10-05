# Managed PostgreSQL vmmd admission: internal KVM diagnostics

Date: 2026-10-01 UTC (2026-10-02 in Europe/Istanbul).

ADR-394's two new metal regressions passed twice with real Firecracker guests.
The focused rerun passed all four required checks, and every pre/post-phase
leak check passed. The full package remains red: its concurrent boot test also
fails on the pre-change baseline. Supported native lifecycle acceptance and
customer cutover activation remain pending.

## Source and host

- Validation snapshot: `d7d1ac1e34709c9626297f242992229f5fe34649`, tree
  `bbd416e9d1e1781295e5b2d41bf89ab3702f14fd`. The final commit retains these Go
  sources; subsequent changes add documentation and evidence.
- Baseline: `0efa82d70fa3bda609aa3b119473e576d726087a`.
- Source archive SHA-256:
  `53d1161f6901dbc3fa7cae06d934c5d2a373e5fe2c8678af3068c61c1d2d858e`.
- Baseline archive SHA-256:
  `a68a87870023a7941180e472cc8a608cf8b6c759446829b25af8d97f12b5c182`.
- GCP project `gregale-prod`, zone `us-east1-b`, instance
  `gregale-internal-test-1`, labeled `environment=test`, `fleet=excluded`,
  `purpose=internal-tests`.
- Linux `7.0.0-1011-gcp`, x86_64, `/dev/kvm`, Go `1.25.13`, Firecracker/jailer
  `1.7.0`, machine `n2-highmem-2` (two vCPUs).
- GCE `enableNestedVirtualization=true`. CLAUDE.md excludes nested virtualization
  from supported native acceptance. These results are internal diagnostic
  evidence; the reference SSD latency gate was disabled.

Both archives were checked by SHA-256 before extraction. Runs used root,
transient systemd units, the designated acceptance marker and the shared
`/var/lock/faas-builder-acceptance.lock`. Each run waited for earlier acceptance
jobs; no earlier job was interrupted. Build scratch used a private four-GiB
tmpfs because the host disk was nearly full. The node had no installed Gregale
daemon units; the full runner's wrapper skipped stopping absent units.

## Full package

`scripts/ci/run-native-metal-smoke.sh` ran the unfiltered
`make test-metal PKGS=./pkg/fcvm` package with `-race`, then its six-test
private mount/network namespace batch. Exact-source guest-init and immutable
two-drive busybox HTTP fixtures were built for the run.

| Phase | Passed | Skipped | Failed |
| --- | ---: | ---: | ---: |
| Metal-tagged fcvm package, including portable tests | 591 | 19 | 2 |
| Namespace batch | 6 | 0 | 0 |
| Total | 597 | 19 | 2 |

The new `TestMetalAppAdmissionFence` and `TestMetalDestroyJoinsLateResume`
passed in 1.93 s and 1.04 s. The guard callback models the held durable fence;
these tests exercise real guest boot, pause, resume and teardown. They do not
attest a deployed node's PostgreSQL configuration or fleet drain ownership.

The two failures were:

- `TestTriggerExtensionHookWire`: the initial scratch prefix made the Unix
  socket path exceed Linux's socket path limit (`bind: invalid argument`).
  This was a test invocation error, repaired by using `/run/mpg-bbd416e9`.
- `TestMetalBoot50Concurrent`: readiness deadlines while booting 30 guests
  concurrently. The test's name retains its earlier 50-guest wording. It
  failed after 31.23 s on this two-vCPU nested host.

The 19 skips identify missing specialized fixtures or isolated test modes in
the [full log](full-run.log). Pre/post leak checks reported zero leaked
namespaces, TAPs, jails, cgroups, Firecracker processes and mounts. The full
runner restored service state and removed its fixture staging.

## Short-path rerun and baseline comparison

The follow-up used the same source snapshot, kernel, Go version, Firecracker
version and two-drive fixture recipe, with a shorter tmpfs scratch path.
`guest/init` is identical in the validation snapshot and baseline.
`make test-metal` built each checkout's own static vmmd/helper binaries and
ran each phase with `-race -count=1`; filters were supplied through `RUN_REGEX`.

| Check | Result |
| --- | --- |
| `TestMetalAppAdmissionFence` | PASS, 1.87 s |
| `TestMetalDestroyJoinsLateResume` | PASS, 1.03 s |
| `TestTriggerExtensionHookWire` | PASS, 0.00 s |
| `TestMetalHelloBoot` | PASS, 0.92 s |
| Changed source: `TestMetalBoot50Concurrent` | FAIL, readiness deadlines, 31.19 s |
| Baseline: `TestMetalBoot50Concurrent` | FAIL, readiness deadlines, 31.09 s |

The focused phase executed four tests with zero skips or failures. The
concurrent test failed on both versions under the same fixtures and settings,
so this observed failure predates ADR-394. Host capacity and nested
virtualization are plausible contributors; this run does not isolate their
individual effects. Every phase and final cleanup passed `make leakcheck`.
The comparison unit exited nonzero because both concurrent phases failed.
See the [follow-up log](recheck.log) for the separate phase outcomes. Committed
logs normalize terminal carriage returns and trailing whitespace only;
`SHA256SUMS` covers those copies and `RAW_SHA256SUMS` records the original
downloaded log bytes, retained with the local validation artifacts.

Portable unit tests and focused race checks also passed for the changed
manager, vmmd startup/adapter, scheduler client and RPC error paths. Changed
packages passed lint; text encoding, shell quoting, sealed env scope, ADR
number uniqueness and core test citation gates passed.

The next cutover prerequisite remains confirmed teardown/drain receipts and
stale-owner rejection before admission reopens. This evidence does not enable
customer activation or replace supported native acceptance.
