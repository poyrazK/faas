# ADR-20261109120000: A restored guest's monotonic clock advances by the time it was parked

- Status: Accepted
- Date: 2026-11-09
- Related: ADR-022 (resume hook steps the wall clock), ADR-642 (kvm-clock
  guest timer pins), ADR-983 (Firecracker 1.17), ADR-907 (measured
  `clock_realtime` on load), ADR-20261107160000 (snapshot contract for plain apps)

## Context

The guest keeps time with kvm-clock (ADR-642). Firecracker before v1.16 set the
restored kvm-clock with `KVM_CLOCK_REALTIME` whenever the capture carried the
flag, so a restored guest's `CLOCK_MONOTONIC` and `CLOCK_BOOTTIME` jumped
forward by the host time elapsed since capture. Firecracker v1.16 (#5809)
stopped doing that by default and added the `clock_realtime` field on
`LoadSnapshot` to opt back in. ADR-983 moved the fleet to v1.17 without the
opt-in, so since that upgrade a restored guest's monotonic clock resumes where
the capture left it. The resume hook (ADR-022) steps only the wall clock with
`settimeofday`.

A park of three days therefore looks like a few seconds to everything in the app
that measures time with the monotonic clock:

- TTL caches (Node `lru-cache`, Java Caffeine and Guava, Python `cachetools`,
  the JVM DNS cache) treat entries cached before capture as fresh on every
  wake;
- timers (`setInterval`, Go `time.Ticker`, Java schedulers) that refresh
  tokens, keys or configuration fire as if no time had passed, so a token that
  expired while parked is used until the timer catches up;
- Go deadlines computed as `time.Now().Add(ttl)` and checked with
  `time.Now().After(...)` compare monotonic readings, as do connection pools'
  idle and lifetime limits.

Plain apps cannot fix this themselves: `CLOCK_MONOTONIC` cannot be set from
inside the guest, and the docs never mention it.

ADR-907 measured `clock_realtime` on the native test host: restore latency
184/218 ms p50/p90, within noise of the baseline. The guest kernel already
tolerates the jump: before v1.16 production restored with it, and the RCU stall
warnings, NMI watchdog and hung-task detector it would trigger are disabled
(`guestStallWarningArgs`, `coldBootArgs`).

## Decision

vmmd sends `"clock_realtime": true` on every `/snapshot/load`, including
warm-pool (paused) restores and verified runtime-snapshot loads.

- **Where.** Only when the detected Firecracker is 1.16 or later and the host
  clocksource is `tsc`. KVM records the realtime flag in a capture only when the
  host keeps time with a stable TSC; Firecracker refuses `clock_realtime`
  against a capture without it (`ClockRealtimeNotInState`).
- **Refusals.** A refused load fails like any other `/snapshot/load` error: the
  wake cold-boots and schedd marks the snapshot stale, so the next park
  recaptures it on a host that records the flag. That costs one cold boot per
  capture taken on a host without the flag (for example after a cross-node
  move). After three refusals in one vmmd process, vmmd stops sending the field
  and logs an error, so a host that does not record the flag despite the `tsc`
  check never loops through cold boots.
- **Switch.** `disable_restore_clock_realtime` in `vmmd.toml`;
  `FAAS_RESTORE_CLOCK_REALTIME` overrides TOML in either direction. The
  startup log line reports `restore_clock_realtime`.
- **Unchanged.** The resume hook still steps the wall clock, reseeds and runs
  `after_restore` before readiness. The verified-load check accepts the field
  only as the literal `true`.

## Consequences

- Monotonic time in a restored app again reflects real elapsed time, as it did
  before ADR-983: TTL caches expire, refresh timers fire on the first tick after
  a wake, and pools retire connections older than their limits.
- Every overdue timer in the guest fires at once on restore. That was the
  behavior before v1.16; the guest's stall detectors are already off.
- Function migration restores use the capture host's realtime reference; with
  NTP-synced hosts the error is the clock offset between them.
- Hosts without a `tsc` clocksource keep the frozen monotonic clock. The
  startup log shows which hosts those are.

## Validation

- Unit: version gate, clocksource detection, load body with and without the
  field, verified-load acceptance of the literal `true` only, and the refusal
  limit (`pkg/fcvm/restore_clock_realtime_test.go`); config switch and env
  override (`cmd/vmmd/config_test.go`).
- Metal, before release: restore a probe that records `CLOCK_MONOTONIC` before
  capture after a park of at least 60 s, and assert the restored reading
  advanced by at least the parked time; run the restore leakcheck.
