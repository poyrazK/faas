# ADR-604: Execution-specific Operations preview admission

## Status

Implemented with admission closed — 2026-10-06. Native and fleet qualification remain required before activation.

## Context

The bounded preview policy selects account, app, environment and customer.
Workflow and Job adapters now share those boundaries with HTTP Operations.
An HTTP cohort grant must not also admit an unqualified native execution family.

## Decision

Add an optional `execution_kinds` allowlist to each version-1 cohort. Values
are exactly `http`, `workflow` and `job`, without duplicates. Omission or JSON
null preserves an HTTP-only grant. An explicit empty array, unknown value,
wrong type or duplicate closes the policy. Transaction-receipt contracts remain
HTTP. Conflicting adapter declarations have no admissible execution kind.

Classify the immutable definition, never customer request input or headers.
Check the kind for direct registration and customer submission, gateway route
admission, source/scan registration and deployment retries. A mixed source
deployment must have every declared kind allowed before manifest mutations or
build enqueue. Batch checks use one current-file snapshot. Existing plan,
ownership, trust, storage and window checks continue to apply.

Doctor observes cohort membership and each selected definition's execution
allowlist from the same policy snapshot. Definition-specific `execution_preview`
checks include `execution_kind` and stable sanitized blockers. The CLI renders
the type; all SDK wire models expose it. A Job-only cohort can report its Job
definition eligible without an HTTP grant. A deployment-wide observation is
blocked if any selected definition is excluded. Eligibility is an observation,
not runtime qualification or a reservation.

Closing one kind rejects new registration and submissions, including idempotent
resubmission. Existing reads, events, execution reports, files, completion
delivery, cancellation and account recovery remain outside new-work admission.
Accepted execution authority and recovery rules do not change. An admission
decision made before atomic policy replacement can still commit afterward.

## Compatibility and rollout

No schema migration, new quota, VM lifecycle change or production grant is
introduced. Update every serving API and gateway node before using native
allowlists. Older binaries reject the new field as unknown and fail closed;
they cannot enforce the HTTP-only interpretation of an omitted field.
Use an explicit HTTP allowlist during a mixed-binary rollout and preserve
retained-work dependencies. Policy replacement remains per-node and atomic;
fleet-wide distribution and qualification are separate operator responsibilities.

## Validation

Exercise policy defaults, isolated and mixed grants, invalid allowlists,
adapter classification, current-file rollback, API/gateway parity, mixed source
and retry rejection, doctor projections, CLI/SDK rendering and retained native
file reporting/downloads in both stores. Native KVM and leak qualification
remain pending and no production policy is enabled by this change.
