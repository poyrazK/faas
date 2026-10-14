# ADR-1260: Initial-failure policy dry run

Status: Accepted (2026-10-12)

## Context

ADR-1259 provides evidence-linked first recovery, but operators cannot evaluate
explicit policy filters before saving a first-recovery plan. Existing durable
policy reviews require a recovery parent job; initial failures have no parent.

## Decision

Accept `initial_failure` in preview policies with explicit failure statuses.
The first-recovery CLI accepts a candidate policy file and evaluates enabled,
trigger, workflow and status filters against the live evidence-backed preview.
Excluded or blocked candidates cannot save a local recovery plan through this
option. Existing native submission and admission checks remain authoritative.

This dry run does not reserve cooldown or hourly budget, persist reviews, or
create jobs. Execution pressure is reported as not evaluated. Automatic retry
continues to require saved_failure exclusively. Policy workers continue to
prepare parent-job reviews only; initial failures are evaluated explicitly by
operators through this CLI until a parentless durable admission path exists.

## Consequences

Operators can test policy selection before manual submission without inventing
parent recovery jobs. Automatic first recovery, budget reservations, durable
initial-failure reviews and policy-version-fenced admission remain subsequent work.
