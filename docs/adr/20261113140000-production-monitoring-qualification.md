# ADR-20261113140000 · Production monitoring qualification receipts

- **Status:** accepted for implementation; production execution pending.
- **Date:** 2026-11-13
- **Extends:** [receiver evidence](20261113090000-notification-receiver-evidence.md)
  and [post-deployment verification](20261113130000-post-deployment-monitoring-evidence.md).

Add a manually dispatched `monitoring-qualification` workflow with `prepare`,
`exercise` and `record` modes. Default to the read-only preparation mode. No
scheduled job, deployment gate or ordinary operator read sends test messages.
Reuse existing GitHub deployment environments, pinned control-plane SSH and
the existing authorized operator bearer. Credentials travel on stdin to a
reviewed Python helper; use fixed loopback API endpoints, no redirects or
proxies, and bounded reads, requests and subprocesses. Never read receiver
configuration or secret files or return provider error text.

Prepare requires fresh independent receiver intent and a working collector.
Previously unobserved delivery success is permitted because this procedure
exists to qualify those routes. Bind every expected group to the fleet,
active-release pointer, sorted inventory digest, workflow run and attempt.
Require the page and heartbeat roles. Include warn when configured. Unknown
or inconsistent inventory cannot become empty passing coverage.

Exercise injects one page and, when expected, one warn alert into the deployed
Alertmanager. Both alertname and family contain the unique run identity;
distinct families prevent page-over-warn inhibition. Preserve the production
route tree, grouping policy, transport adapters and `send_resolved` settings.
Each alert expires after 15 minutes. Hold for 90 seconds, then explicitly
resolve only those alerts. Attempt resolution after every submission attempt,
including uncertain delivery or cancellation. A killed runner may prevent
cleanup; expiry bounds the remaining firing period. Never stop services,
modify configuration or silence/replace `FaasWatchdog`.

Provider delivery and human receipt cannot be inferred from completed-request
metrics or an accepted alert POST. Keep all receiver receipt checks unknown
until an operator supplies test-bound firing/recovery observations. Separate
recording accommodates the deployed hourly warn grouping interval without
holding a workflow runner for an hour. Every received observation needs its
time and a unique SHA-256 receipt fingerprint, plus an explicit assertion that
all destinations aggregated by that receiver/integration were observed. Store
no addresses, raw receipts or provider URLs. Negative observations remain
visible as failed checks; missing observations remain unknown.

For external watchdog qualification, use a separate dedicated deadline check
with the same independently delivered notification settings. Record its initial
ping, missing-ping alarm and recovery. Require the alarm timestamp to follow
the configured qualification deadline. A provider's test-notification facility
alone cannot establish deadline behavior. The report always discloses that
live-watchdog interruption and the production deadline were not exercised.

Record validates the source workflow's identity/event/commit through GitHub,
the selected run and attempt, and its completed exercise/cleanup operations.
Recheck current release and inventory before accepting receipts. Require an
operator assertion that receiver configuration has not changed: the public
group inventory cannot identify destination changes within the same group.
Reject replay across test identities and stale evidence. Preserve the original
24-hour validity deadline; recording cannot extend it. Retain source hashes,
exercise and recorder commits, actor and recording run for review.

Complete evidence is labelled `operator_attested`, never automatic receipt
verification. Root state is failed for reported missing receipts and unknown
for incomplete/unavailable evidence. Exit 0 only for complete attestation, 1
for reported failure, 3 for unknown, and 2 for local configuration/report I/O.
Initialize an unknown owner-only JSON report and attempt artifact upload on
all workflow outcomes, with 30-day retention. Artifacts are immutable run
records; expiry and later receiver changes invalidate current qualification.
This procedure bypasses Prometheus rule evaluation and is separate from the
existing isolated Prometheus-to-webhook drill and continuous health checks.
See the [operator procedure](../ops/monitoring-qualification.md).
