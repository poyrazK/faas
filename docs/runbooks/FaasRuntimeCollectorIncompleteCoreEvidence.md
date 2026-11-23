# FaasRuntimeCollectorIncompleteCoreEvidence

## Symptom

An apid has incomplete evidence for at least half of one core group's
evaluations over 30 minutes, with at least 10 evaluations in that window.
Both conditions have held for 15 minutes while Prometheus can scrape apid.
The group identifies identity, readiness, process, or loop health evidence.

## Check

1. Open **Runtime Diagnostics** from the warning to select the affected apid
   and evidence group. Check the evaluation count in **Incomplete report rate
   by evidence group (30m)**. Use **Core evidence gaps by reason (30m)** to
   distinguish missing, stale, conflicting, or unavailable evidence. Compare **Time since the
   last complete runtime report by apid** and **Runtime telemetry coverage
   by apid** for the same instance.
2. Inspect the matching evidence in the deployment verifier's runtime report.
   Use `--runtime-diagnostics none` to focus a fresh protected read on core
   evidence. Check which targets have missing, stale, conflicting, or
   unavailable evidence.
3. Review **Non-successful source reads by group (5m)** for query failures.
   Use **Source read failures by reason (5m)** for transport, HTTP, and
   response errors. When reads succeed, check that the affected targets
   export the expected series and that the observations are fresh for the
   selected profile.

## Verify

Confirm the group returns fresh evidence and its incomplete rate drops below
50% with at least 10 evaluations in 30 minutes. In the **Runtime collector
warning timeline**, **Insufficient evaluations** means the group is below
that floor. Check **Runtime telemetry coverage by apid** for missing metrics
or failed scrapes.
