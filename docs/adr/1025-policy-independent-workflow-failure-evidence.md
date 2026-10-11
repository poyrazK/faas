# ADR-1025: Policy-independent workflow failure evidence

Accepted, 2026-10-11.

Policy reviews only capture candidates selected by an active policy. Widening its workflow or status filters previously omitted never-reviewed failures, even with ADR-1024 durable review evidence.

Capture terminal `failed` and `dead` workflow transitions prospectively, regardless of policy existence, enabled state or filters. Operator cancellations and successful outcomes are excluded. The first observation of an app/run/admitted generation wins. Evidence includes workflow, generation, exact failure status and observation/completion times; it excludes inputs, outputs, error text and definition snapshots. No backfill or mutation-time inference from removed runs is performed.

PostgreSQL captures through a workflow-row trigger in the terminal mutation transaction. Per-app advisory serialization ensures first observation and capacity pruning commit together. A capture failure rolls back the terminal mutation; ordinary transaction error handling applies. Memory captures under its workflow mutation lock. App deletion removes evidence, but workflow/run-history pruning does not. Repeated terminal updates cannot recreate evicted records.

The dedicated inventory has a separate 10,000-entry/app, 30-day observation-age budget. Database capture bounds mirror centralized API limits and are checked by tests. New captures prune expired and oldest entries; the existing scheduler retention sweep also expires inactive inventories with separate bounded batches, so a review-history backlog cannot starve failure retention. Reads hide expired entries without deleting them.

Historical evaluation reads at most 500 oldest matching failures plus one truncation probe in the same memory lock or PostgreSQL repeatable-read snapshot as review/application evidence. Candidates with sampled saved-failure retry reviews reuse review preflight facts and are not counted twice. Unreviewed failures are included with `failure_inventory` provenance, a stable evidence identifier, and unknown retry eligibility after policy filters. Initial failures are included: capture alone does not establish a recovery parent, quota availability, safe resume or admission eligibility. Missing checks never charge simulated preparation budgets.

The response and CLI show capture start, partial coverage, sampled captured/reviewed/unreviewed counts, missing eligibility proof and independent failure truncation. Review correlation uses the bounded review sample. Capacity/retention/pre-deployment gaps cannot prove the absence of other failures, so coverage stays explicitly partial. Review/job/version/position associations are optional for inventory candidates. Capture does not execute retries, create policies/reviews/jobs or send notifications.

Downgrade refuses while inventory evidence remains retained.
