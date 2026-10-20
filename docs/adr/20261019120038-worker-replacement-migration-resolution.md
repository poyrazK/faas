# ADR-20261019120038: Resolve uncertain migrations against worker replacements

## Status

Implemented locally; native KVM qualification pending. Production flags remain off.

## Context

A coordinated promotion can leave an uncertain migration outcome while the selected
baseline remains active. Operators may need corrected application code rather
than resuming the original candidates. Resolution previously excluded all worker
replacements, even after a successful verified repair promotion.

## Decision

Allow a succeeded, currently selected, configuration-synchronized replacement
promotion with one supported push worker and no migration command. Preserve the
existing explicit database-inspection attestation and scheduler-confirmed teardown
of the exact original migration executor. Never rerun SQL or rewrite its outcome.

Finalization rechecks the complete selected graph, destination configuration and
runtime fingerprints, fresh HTTP dependency probes, captured worker consumer
policy, and current owned worker startup/configuration proof. PostgreSQL holds
worker instance and consumer rows while making the final decision.

Read delivery health directly from the owned ledger. Compare the selected
replacement's deliveries since the recovery request against deliveries by that
same deployment during the existing baseline window immediately before the
request. Both windows require the normal minimum complete attempts and existing
error, retry, latency and backlog thresholds. Jobs from another deployment or
binding do not count. This avoids depending on an old worker that may have been
retired, or trusting a completed promotion's stale success counters.

Missing startup, insufficient traffic or unhealthy deliveries leave recovery
verifying until sufficient evidence arrives or its existing deadline expires.
Configuration, consumer or destination drift conflicts and blocks reconciliation.
The immutable recovery record includes the observed worker, delivery samples and
window timestamps alongside existing runtime fingerprints. Matching request IDs
return the original result after later intent changes; altered attestations conflict.
The original failed promotion and migration task remain unchanged.

No new API field, schema migration, scheduler controller or boot flag is needed.
Recovery remains available independently of new-promotion admission switches.
Add a reviewed `worker-replacement-resolve` receipt to worker qualification,
which coordinated qualification also requires;
no approval artifact is generated automatically.

## Validation

Portable memory and PostgreSQL cases cover full frontend/API/worker replacements,
no SQL replay, missing startup, unhealthy/insufficient delivery evidence, wrong
version attribution, consumer changes, HTTP failure, configuration changes,
teardown, and idempotent recovery. These do not prove native guest execution.
The coordinated native acceptance runbook defines the required runtime exercise
before production enablement.
