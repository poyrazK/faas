# ADR-719: explicit customer Operation workflow state

Status: accepted for the internal HTTP implementation.

## Context

ADR-715 retains application-reported business milestones, and ADR-716 maps
those facts into named workflows. The timeline can show observed events, but
applications need a direct way to report the current business state they have
committed.

## Decision

Applications may declare a bounded set of state names for each workflow in the
source manifest. A customer Operation transaction can report a state for a
workflow instance along with the application's business write, milestone
facts, and result receipt. The Node SDK allocates a monotonically increasing
revision per tenant, workflow, and instance inside that transaction. It saves
reports in an app-side outbox and publishes them after commit under the active
execution fence. Recovery replays pending reports using stable report IDs.

Gregale validates each report against the immutable workflow vocabulary pinned
to the Operation definition. A report is idempotent by report ID and payload
fingerprint. The current-state projection accepts a newer revision and ignores
late older revisions. Business-reference reads return the latest explicit
state for each workflow instance, and the dashboard displays it with its
revision and platform update time. An unreported state remains absent.

## Consequences

State names, revisions, and times are application-owned business data; the
projection does not infer a state from a milestone, Operation outcome, or
execution status. Vocabulary changes affect newly pinned Operation
definitions. Current snapshots are retained with the existing customer
business-reference scope and access boundaries.
