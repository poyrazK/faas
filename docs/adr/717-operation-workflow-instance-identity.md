# ADR-717: identify workflow instances from public milestone facts

Status: accepted for the internal HTTP implementation.

## Context

ADR-716 groups observed facts by workflow name and business reference. When the
same process runs more than once for one reference, the timeline can combine
facts from separate runs.

## Decision

Each declared workflow step must include `instance_id_from`, a JSON Pointer to
a stable run ID in that step's milestone payload. The pointer is pinned with
the immutable Operation definition. Gregale requires the selected value to be
a nonempty UTF-8 string no longer than 256 bytes and without control
characters, and returns it as `instance_id` on the observed workflow mapping.
Applications use one run ID across all Operations that participate in one
business process instance.

The dashboard groups observations by workflow name and instance ID, within the
existing account, customer, app, environment, and business-reference filters.
Identity stays in the validated public payload and immutable definition, so
this projection requires no additional retained field. Missing facts remain
unknown and do not imply a status or trigger application behavior.

## Consequences

Changing an instance ID pointer changes the affected Operation definition
revision. Each milestone mapped to a workflow must include a valid selected ID;
the same ID must be propagated through all steps in one process run. A run ID
is correlation metadata, not an authorization token. The workflow timeline
continues to be bounded by the selected milestone feed page.
