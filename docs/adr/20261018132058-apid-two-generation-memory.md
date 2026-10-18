# apid memory for two-generation reloads

Date: 2026-10-18
Status: Accepted (owner asked for apid deploys with zero downtime regardless of apid's memory use)
Related: ADR-1580 (zero-downtime daemon restarts, two generations), spec §13 (RAM budget ledger), ADR-068 (apid socket activation)

## Context

A reload runs two apid generations in one cgroup for up to 30 minutes
(ADR-1580). The successor starts only while the cgroup uses at most 45 % of
its `MemoryMax`. apid's `MemoryMax` was 256 M, so a reload fell back to a
restart above 115 MB.

Production use, from the control plane's systemd journal and cgroup counters
over the 14 days to 2026-10-18 (per-run cgroup peaks, newest last):

| Runs | Peak per run |
|---|---|
| 9 of 15 | 51–94 MB |
| 6 of 15 | 128, 170, 184, 198, 256 and 257 MB |

The current run is at 84 MB (76 MB RSS) with a 172 MB peak. Twice, on
2026-10-15 and 2026-10-17, the kernel OOM-killed apid at the 256 MB cap
(`anon-rss` about 254 MB). So the cap was already too small for one process,
and the 115 MB hand-off threshold sat inside apid's spike range.

The control-plane slice in production (`faas-cp.slice`, 3 GiB from the
control-plane role) used 361 MB with a 913 MB peak. The spec's ledger
(§13, 6,144 MB) gives apid 256 MB and keeps 2,304 MB of slack, which must hold
the guaranteed builder VM (2,048 + 8 MB) on a single box.

## Decision

1. apid's `MemoryMax` rises to 512 M (`api.APIDMemoryMaxMB`). Two generations
   fit: a successor spiking like the largest observed run (257 MB) beside a
   draining predecessor at steady state (≤ 94 MB) uses about 350 MB.
2. Each apid process gets `GOMEMLIMIT=256MiB` (`api.APIDGoMemLimitMB`), a soft
   per-process bound: the Go collector works to keep each generation near
   half the cap. It is not a hard limit; the cgroup cap still is.
3. The hand-off guard keeps its 45 %, now 230 MB, above every steady-state
   run measured. A reload during a spike no longer refuses at once: the old
   generation returns freed heap to the OS and checks again every 2 s for up
   to `api.HandoffMemoryWaitSeconds` (60 s), since apid's spikes last minutes.
   deployctl waits for the memory wait plus the readiness bound before it
   restarts instead.
4. The §13 ledger moves apid from 256 MB to 384 MB and the slack from 2,304 MB
   to 2,176 MB. The slice total (6,144 MB, `api.ControlPlaneReserveMB`) and
   tenant admission are unchanged, and the slack still holds the builder VM's
   2,056 MB.

apid's API listener is already behind `faas-apid.socket` (ADR-068), so a
restart fallback queues API connections instead of refusing them; this ADR
adds no socket.

## Consequences

- apid stops being OOM-killed at the spikes measured so far.
- The first deploy of this unit is a restart: deployctl reloads only a unit
  unchanged since it started. Later binary-only deploys reload.
- A spike that outlasts 60 s still turns a reload into a restart. API
  connections then queue on the socket, and requests longer than the
  10 s drain are cut, as before.
- Two generations both above their soft limit can still reach 512 MB; the
  kernel then kills one, and `OOMPolicy=continue` keeps the other serving.
- apid's internal gRPC listeners (advisory, githubd bridge, app errors,
  request telemetry, spans writer) are not socket-activated. Their callers see
  refusals during a restart fallback, but not during a reload.
- apid exports no Go process-memory metrics, so this sizing rests on
  systemd's per-run peaks. Exporting `process_resident_memory_bytes` would let
  the next review use history instead.
- Production's control-plane slice is 3 GiB while §13 says 6,144 MB. That
  drift predates this ADR and is not changed here; its measured 913 MB peak
  leaves room for apid's larger cap.
