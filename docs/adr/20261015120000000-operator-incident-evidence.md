# ADR-1746: Operator incident evidence

Status: Accepted, 2026-10-15. Extends ADR-091 and operator observability fidelity.

## Decision

Add `GET /v1/admin/obs/incidents/{kind}/{id}/evidence` and
`gregalectl obs evidence --incident type/resource-id`. The endpoint uses the
existing admin-scope, MFA, and email-allowlist gates. It reads existing state;
it does not create incident records or change controller ownership.

Resource IDs are canonical UUIDs for `deployment`, `job_run`, and `compute_node`.
Platform alert IDs are the 64 lowercase hex characters after `platform_alert/`
in the inbox ID. Existing label hashes and `ALERTS_FOR_STATE` activation times
are preserved. Resources remain inspectable after recovery. A deleted/missing
resource returns 404. Platform alerts now follow
[durable alert history](20261015125221000-operator-alert-history.md): retained episodes
remain inspectable after resolution. A failed local source returns 503.

Resource evidence includes current status, a deployment failure code when
present, and the last 24 hours of exact-resource events, deployment audit rows,
and directly targeted operator intents. Account-subject operator events also
match their explicit `target_id`, `run_id`, or `deployment_id` reference. A
partial expression index supports this lookup for historical rows without
an account-wide scan. An account match alone never associates an event with
an incident. Events matching both subject and reference are included once.
Each indexed SQL branch reads at most `limit+1`
rows before merging, with a final bounded limit. Default history limit is 25,
maximum 100, and an over-read makes `truncated` explicit. History order is
`at DESC, source DESC, id DESC`. Bigint IDs are strings. The response omits raw
payloads, actor identities, image/environment data, and free-form errors.
Valid trace IDs link to the existing bounded trace lookup.

Prometheus alert annotations come from the bounded, deadline-limited alerts
API and are matched by labels, excluding `__name__` and `alertstate`. Only
the rule's documentation URL and validated resource UUIDs are projected;
annotation descriptions and arbitrary label bags are omitted. Runbook links
must be local `/docs/` paths or HTTPS links to this repository's `main/docs/`
tree. Metadata failures retain the firing alert and generic runbook fallback,
with `prometheus_alert_metadata` in `unavailable_sources`. Resource evidence
does not depend on Prometheus. Retained alert evidence also works without
Prometheus. Before the first successful collection, live-only alert lookup is
explicitly unknown and does not claim historical coverage.

## Validation

Regression coverage checks memory/PostgreSQL history agreement, exact-resource
isolation, limits/windows, trace links, recovery lookup, malformed references,
authorization, source failures, annotation degradation, safe runbook URLs, and
CLI JSON/human output. This backend contract is additive and requires no frontend changes.

The additive migration creates one partial index on `events`. It runs in the
normal migration transaction, briefly blocking event writes during index
creation, following the existing event-index migration convention.
