# ADR-20261113100000 · Independently detect missing scheduled canaries

- **Status:** accepted for implementation; external monitor activation pending.
- **Date:** 2026-11-13
- **Extends:** [observability on the control plane](20261023124955-observability-on-the-control-plane.md).

The six-hourly production deploy canary pages when a running workflow fails.
A disabled schedule, pending job, runner loss or cancellation can prevent that
page from ever being emitted. A dedicated external missing-success deadline
must observe the canary independently of Gregale's control plane.

Successful first-attempt scheduled runs POST to the success URL held in the
GitHub production environment secret `CANARY_HEARTBEAT_URL`. Manual runs,
reruns, failed or incomplete probes and unsuccessful jobs cannot refresh the
deadline. The workflow condition and helper independently enforce eligibility.
No start or failure ping is sent. Keep the six-hour cadence and 45-minute job
limit; configure an external six-hour interval with two hours of grace.

The helper uses verified HTTPS, rejects redirects, bypasses proxies and bounds
transport retries. It sends no request body or diagnostic payload and logs no
URLs, response bodies or raw exceptions. A missing URL or transport failure
fails an eligible run; the existing failure route names the heartbeat failure
separately from a failed deployment. No new platform API, metric or quota is
introduced.

The external monitor has its own notification delivery and a check used only
by this workflow. A successful ping does not prove the deadline is armed or
that an operator received an alarm. Provision the secret before rollout, then
verify the first scheduled receipt and qualify an independent missed-ping
alarm and recovery. Activation remains pending until those receipts exist.

Tests cover run eligibility, failure handling, bounded retries, real loopback
TLS, redirect refusal, proxy bypass and credential redaction. The
[operator procedure](../ops/synthetic-canary-heartbeat.md) defines setup,
interpretation and recovery.
