# ADR-716: read-only customer Operation business workflows

Status: accepted for the internal HTTP implementation.

## Context

ADR-715 retains schema-validated business milestones and associates them with
an Operation's immutable business reference. Applications can report facts
across several Operations for one entity, but clients do not know how those
facts map to named business processes or their step order.

## Decision

Applications may declare `operation_workflows` in the source manifest. Each
workflow names ordered steps and maps every step to an Operation definition
and one of its declared transaction-backed milestones. Deployment resolves the
mapping into the immutable definition revision. A milestone read returns only
the workflow step mappings that match that fact's name.

The dashboard groups observed mappings by workflow for the selected business
reference, links each fact to its Operation, and retains the fact payload. The
Go, Node, and Python APIs expose the same metadata. The projection is
read-only: it does not infer status for a missing fact or trigger application
behavior.

## Consequences

Changing workflow labels or ordering changes affected Operation definition
revisions. Retained facts keep the mapping pinned to the definition that
published them. Workflows can span multiple Operations and business references
remain tenant- and environment-scoped by the existing milestone feed. A
workflow view is limited to the feed page selected by its cursor.
