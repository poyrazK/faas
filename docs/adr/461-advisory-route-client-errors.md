# ADR-461: Advisory watched response codes

## Status

Accepted — 2026-10-03.

## Context

Aggregate and customer 5xx/latency comparisons can miss authorization, validation,
lookup or rate-limit regressions that return 4xx. Such responses can also be
expected application behavior, so treating every 4xx as a rollout failure would
misrepresent healthy traffic. Developers need selected-code evidence compared
against the same stable behavior, with explicit sampling and attribution limits.

## Decision

Extend existing revisioned exact route selectors with optional `watch_statuses`:
distinct codes from 401, 403, 404, 422 and 429, bounded to five per route. Reuse the
existing configuration JSON, validation, revision fence and stage/configuration
anchor. Clone nested selectors at memory-store boundaries. No schema migration,
telemetry ingestion change or VM lifecycle change is required.

Read aggregate per-code observations only on live reports with watched selectors,
inside the existing repeatable-read transaction. Opt-in customer queries return
equivalent per-identity evidence using recorded, account/app-scoped attribution.
Keep immutable deployment attribution, normalized method/path labels, closed
windows and publisher weights. Pad missing sides with zero observed requests;
never substitute another deployment or identity.

Evaluate each code independently with the existing error thresholds: 20 requests
per deployment/window, two candidate responses, 5% rate, three times stable and a
five percentage point increase. Confirm the same code across both windows. Return
separate aggregate advisory summary fields and per-route code evidence, and
include these signals in the already advisory customer summary. Cohort health
status remains the existing 5xx/latency verdict. After candidate 5xx counts, rank
bounded customer output by selected-code candidate response counts before volume.
Keep full attribution, cohort counts and omitted volumes before output bounds.

CLI live reads validate code inventories, thresholds, counts, rates, verdicts and
window alignment against the aggregate or cohort evidence. Default identity
redaction and explicit details opt-in remain unchanged. Expose additive DTOs in
OpenAPI and all three SDKs.

Canary advancement, automatic recovery and persisted decision evidence continue
to use the original aggregate evaluator and never query/evaluate these signals.
Saved selector configuration may include watched codes, but live 4xx evidence
and customer identities are absent from history, audits and webhooks.

## Consequences

Developers can detect a customer-specific 403 or route-level 422 increase without
manual log joins. Stable expected rejections remain healthy comparisons. All
signals stay advisory, including when route health is enforced. Editing watched
codes resets the shared configuration anchor, which can hold existing enforced
checks until fresh windows arrive. Sparse cohorts, missing deployment attribution
and capped output limit conclusions. Pre-routing gateway rejections are excluded
because they lack immutable candidate/stable attribution; coverage remains
observed_only.
