# ADR-824: Native profiling restore qualification

Status: Accepted

## Context

The deployment acceptance runner needs observed snapshot, restore, epoch and
stale-profile evidence. Application park can reuse an earlier deployment snapshot
and destroy the running VM without invoking its profiling checkpoint. A probe
staged immediately before park cannot survive that path, even if the next wake
successfully loads the earlier snapshot.

## Decision

Profile-enabled applications take a fresh terminal snapshot on park, with the
existing profiling checkpoint flag. The collector acknowledges suspension before
capture; the existing resume path rotates the epoch. Warm capture remains skipped
for profiled applications. Snapshot publication and cold-boot fallback continue
through the existing lifecycle machinery.

The disposable acceptance workload exposes token-protected state and stale-probe
routes only when its dedicated token is configured. These routes read the fixed
loopback profiling bridge and replay a valid, synthetic CPU-format probe staged
before park. Fresh-epoch submission is forbidden. No customer-supplied payload or
forwarding destination is accepted.

The native adapter requires park completion with a snapshot locator, zero resident
instances, an actual `wake.boot_completed` restore event, a retained process nonce
and staged profile digest, a changed epoch and the bridge's specific old-epoch
HTTP 409 response. Receipts retain the observed IDs and event evidence. They are
not inferred from successful readiness or a queued wake alone.

## Consequences

Profiling-enabled parks perform additional snapshot capture and storage work.
Operators should account for snapshot latency and bytes when enabling profiling.
The acceptance drill parks the entire disposable app, requires one baseline
instance and requires baseline to be the live deployment for explicit wake.
Native KVM execution is required to qualify this path; compilation is insufficient.
The synthetic probe verifies admission at the epoch fence. The deployment runner
separately verifies real CPU and request counters after restore.
