# ADR-20261103222000: Project selection and concurrent mutation guards

- **Status:** accepted
- **Date:** 2026-11-03
- **Owner:** apid and state maintainers
- **Amends:** ADR-124, ADR-127, ADR-20261103194624

## Problem

Protected project approvals bound the source but omitted workload filters. An
approved `--only alpha` plan could therefore deploy beta. Persisted exclusions
using allocated app slugs failed to match logical service names, and removing
excluded entries from `--only` could turn an empty selection into a full deploy.
Promotion resume compared scoped checkpoints to global project membership.
Superseded-revision rollback and project deletion checked current state before
their writes, allowing concurrent releases or stage creation to be overwritten.

## Decision

New source plan tokens record normalized explicit only/exclude filters and the
effective persisted exclusion set separately. Protected applies require an exact
match before consuming their approval. Cached source applies inherit omitted
filters; explicitly supplied filters retain the existing unprotected workflow.
Persisted exclusions load during preview and apply and take precedence without
changing the requested only set. If every requested service is excluded, return
409 `project_selection_empty` without deploying another service. Selectors match
logical service names, allocated app slugs and repository roots consistently,
including preservation of an excluded app omitted from later source.

Old tokens without selection may continue unprotected workflows. Protected
approval issuance and apply require a new preview; they fail closed for old
unbound tokens. No guest protocol or database schema changes are needed.

Fresh promotion and resume use the same union of source and target membership.
Third-environment services are ignored; new source or target services invalidate
saved checkpoints. Superseded rollback uses normal activation with an additional
expected-serving guard checked under its existing mutation locks. A concurrent
release or split returns conflict; all artifact and policy checks still apply.
Legacy default and named production serving lanes retain their existing separate
activation boundaries; restoring one cannot change or be blocked by the other.

Customer project deletion uses a checked store operation. Under the same project
lock used by stage creation and clone reservation, it refuses named stages and
unfinished clones, then performs the existing delete. The handler maps raced
conflicts to 409. Raw deletion remains available for internal compensation.
MemStore enforces both guards under its existing global mutex.

## Validation and recovery

HTTP regressions cover protected selection changes and approval reuse after
rejection, legacy-token refusal, empty only selections, persisted app-slug
exclusions and graph resume membership. MemStore and PostgreSQL tests interleave
a newer serving release during rollback and create a stage or clone after a
delete preflight, checking that state is preserved. Existing rollback success
tests and a checked deletion without stages verify normal operations still work.
Rollback of the control plane requires pausing affected project mutations until
the guards are restored; existing deployments and managed resources are retained.
