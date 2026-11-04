# ADR-20261103194624: Environment membership, databases and promotion rollback

- **Status:** accepted
- **Date:** 2026-11-03
- **Owner:** apid and state maintainers
- **Amends:** ADR-20261025111824, ADR-20261103180000

## Problem

Automatic Compose PostgreSQL provisioning used the production database name for
every environment. A stage could therefore bind read/write credentials to the
production database. Project-wide app inventories also made stage-only services
block production safe releases and graph publication, and services in unrelated
stages block promotion. Promotion rollback reported success without moving traffic
back to a retained live revision because marking an already live revision is a
no-op. Folded migration steps silently inherited a different user or working
directory from their serving owner.

## Decision

Production keeps the existing `<project>-<service>` automatic database names.
Named stages use `<project>-<environment>-<service>-<hash>`, truncating the readable
prefix to 46 bytes and appending 16 hexadecimal SHA-256 characters over the
project, service and scope separated by NUL bytes. Names fit the existing 63-byte
provider limit and remain stable and distinct after truncation. Additional stage
databases use the existing quota; quota exhaustion never falls back to production.
Explicit scoped variables and existing bindings remain authoritative. This does
not automatically rewrite or migrate historical shared database bindings.

Release membership follows environment admission. Named environments include
active project apps with a desired workload head or a deployment in that scope.
Production preserves legacy membership and excludes only apps configured solely
in named stages without a production head, deployment or accepted source hash.
Shared app identities and account-wide quota counting are unchanged. Safe source
planning, graph publication and graph fallback validation use that membership in
both stores. Promotion plans include the union of source and destination members:
services confined to a third environment are irrelevant, while a destination-only
service still blocks destructive omission. An empty source cannot be promoted.

Promotion rollback identifies the single deployment receiving all target traffic,
ignoring retained live revisions at zero percent. For a retained prior revision it
uses the existing atomic traffic update with the promotion candidate as expected
serving deployment. A newer release or traffic split produces a conflict. Repeated
rollback recognizes the restored serving revision. Superseded revisions retain
the existing activation path, and rollback with no predecessor clears its owned
candidate, including a candidate already reduced to zero traffic.
If clearing that candidate could activate an unrelated staged revision through
failure recovery, rollback returns a conflict and preserves both revisions.

A folded migration must declare the same `user` and `working_dir` as its serving
owner. Inheritance on only one side is conservatively treated as a mismatch.
Owner selection prefers a compatible waiting service; without one, scanning fails
with guidance to match the settings. Supporting separate task runtime overrides
would require a guest protocol change and is outside this correction.

## Validation and recovery

HTTP regressions cover database separation, stable reuse, long-name collisions,
quota exhaustion, unrelated-stage promotion and destination-only protection.
MemStore and PostgreSQL contracts cover scoped graph membership, safe planning,
fallback validation, retained and superseded rollback, repeated rollback and
preservation of a newer release. Scanner tests cover runtime mismatch rejection
and compatible-owner selection. No schema or VM lifecycle change is required.

Existing deployments retain their artifacts, settings and database bindings.
Rolling back the control plane reintroduces these defects; suspend affected
project applies and promotions while using the prior release. Stage database
names and any explicitly corrected bindings should be preserved across rollback.
