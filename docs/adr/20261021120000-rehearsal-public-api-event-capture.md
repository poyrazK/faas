# ADR-20261021120000 · Rehearsal capture of public-API event publishes

- **Status:** accepted
- **Date:** 2026-10-21
- **Amends:** [ADR-20261020090000](20261020090000-rehearsal-event-capture.md), [ADR-20261021090000](20261021090000-rehearsal-queue-capture.md)
- **Decision:** The run-local platform API also serves `POST /v1/events:publish` and `POST /v1/events:publish-batch`. These publishes enter the same event capture as in-guest publishes. Captured events keep their envelope ID, so a repeated publish of the same source and ID counts once, as it does in production.
- **Why:**
  - **Blocked publishes.** A contained app that publishes with the SDK and an API key was blocked by containment. Only the in-guest event endpoint was captured.
  - **Retries counted twice.** Capture stored no event ID, so a retried publish became two events. Production publishes have a required `id` and deduplicate on source and ID, so the candidate failed the comparison for an ordinary retry.

## Decision

### Run-local routes

`api.svc.gregale` now serves the two event routes beside the queue routes. They follow the same rules:

- **Who it serves.** Contained callers only; every other request reaches the service proxy unchanged.
- **Identity.** The caller is resolved from its source address.
- **Account.** The account comes from the caller's rehearsal policy, never from the request.

### Validation

- **Shared rules.** Both routes and the in-guest path use one function, `events.NormalizeCapturedPublish`. It applies the same envelope normalization, required ID, reserved `gregale.*` sources, account check and schema registry validation as apid.
- **Same status codes as apid:**

  | Failure | Status |
  |---|---|
  | Invalid envelope or reserved source | 400 |
  | Foreign `account_id` | 403 |
  | Schema version unknown, required or violated | 422 |
  | Body over 1 MiB (single) or over the batch byte cap | 413 |

- **Decoding.** Decoding is as lenient as apid's. `PublishEventRequest`'s own unmarshaler ignores unknown fields, so they are ignored here too.
- **Batches.** A batch holds 1–100 events. Each item reports `accepted`, `duplicate` or `rejected` in the public API's result shape.

### Identity

- **Storage.** `scenario_captured_events` gains a nullable `event_id`. Rows captured before the column existed keep NULL and never deduplicate.
- **Matching.** Under the run lock, a capture with an existing source and ID in the same run is compared with the earlier one.
  - The same type, schema version and data, compared as JSON values, is a duplicate. It succeeds without a second event (`ErrScenarioEventDuplicate`): single publishes answer `202`, batch items answer `duplicate`, and the in-guest path treats it as success.
  - A different content is `ErrScenarioEventIdentityConflict`, an `ErrConflict`. It answers `409 Event identity conflict`.
- **Scope.** Identity is per run and per source, so the same ID from another source is a different event.

### Responses

- **Single publish.** `202`, with the public API's receipt body and `Location`. `accepted_at` is the capture time.
- **Header.** Every captured response carries `X-Gregale-Rehearsal: captured`.
- **Receipts are not served.** A later `GET /v1/events/receipt` reaches the service proxy and returns 404.

## Consequences

- **Comparison.** Comparisons are unchanged; event IDs still never compare. A run that retries a publish now matches a run that published once.
- **In-guest dedupe.** In-guest publishes deduplicate the same way, through the same rule.
- **Bounds.** The event bounds of ADR-20261020090000 apply to both paths together: 256 events per run, and 64 KiB of data per event.

## Rejected alternatives

- **A unique index on (run, source, event_id).** The run lock already serializes captures, and a run holds at most 257 rows, so a lookup on the run index is enough. A new index would also add a non-concurrent index build to an existing table.
- **Returning a receipt for duplicates from the first capture time.** Comparisons never read `accepted_at`, so the column would add storage without adding evidence.
