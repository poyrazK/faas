# ADR-20261021180000 · Rehearsal capture of realtime publishes and signals

- **Status:** accepted
- **Date:** 2026-10-21
- **Amends:** [ADR-20261021150000](20261021150000-rehearsal-workflow-capture.md)
- **Decision:** The run-local platform API serves three realtime routes for contained callers:
  - `POST /v1/apps/{slug}/realtime/endpoints/{id}/channels/{channel}/publish`
  - `POST …/channels/{channel}/signals`
  - `POST …/channels/{channel}/activity-scopes/{scope}/signals`

  Each request is recorded in `scenario_captured_platform_calls` as `realtime_publish` or `realtime_signal` and never delivered. Comparisons compare them like other platform calls.
- **Why:**
  - **Blocked calls.** Backend realtime publishing goes to the public API, which containment blocks. A contained candidate's publishes failed.
  - **Uncompared calls.** A candidate that published to the wrong channel, or twice, still passed.

## Decision

### Targets

- **The endpoint must belong to the app the path names, in the caller's account.** Anything else is a 404, as in production.
- **A run member's endpoint.** Named `workload:<name>/endpoint#<n>`, where `n` is the endpoint's creation order within that app. Endpoint IDs of preview copies differ between sibling runs; the order does not.
- **Another app's endpoint.** Named `app:<slug>/endpoint:<id>`.
- **The name is the channel.** An activity-scoped signal uses its scoped channel name.

### Validation

Validation follows apid's realtime handlers.

- **Publish:**
  - The channel must be valid.
  - `delivery` is live or retained.
  - `data_base64` must decode, and the decoded message is at most 1 MiB.
  - `ttl_seconds` and `expected_sequence` require retained delivery. Metadata must be valid.
  - A retained publish needs exactly one Idempotency-Key and at most 4096 decoded bytes.
  - An Idempotency-Key is 1–128 printable ASCII bytes. The same channel and message under a key replays with `Idempotent-Replayed: true`; another message is a 409.
- **Signal:**
  - JSON data of at most 2048 bytes.
  - The name and `ttl_ms` rules, and the ephemeral-frame check.

### Content and responses

- **Compared content.**
  - A publish compares `delivery`, `data_base64`, `binary`, `metadata`, `ttl_seconds` and `expected_sequence`.
  - A signal compares `data`, `name` and `ttl_ms`.
- **Responses.**
  - A publish answers 200 with no subscribers queued. A retained publish reports `durable: true` and no sequence.
  - A signal answers 200 `accepted` from member `backend`.
  - Every captured response carries `X-Gregale-Rehearsal: captured`.

## Consequences

- **Nothing is delivered.** Connected clients receive nothing, and retained history is not written, so no sequence is assigned. A candidate that chains `expected_sequence` from a returned sequence cannot be rehearsed this way.
- **Not served.** These routes pass through to the service proxy, which returns 404:
  - `publish-batch`, which is preview-gated in apid;
  - publisher-credential publishes (`/v1/realtime/publishers/...`);
  - connection and history reads.
- **Not checked.** Endpoint enablement, the per-endpoint `max_message_bytes`, plan gates and the backend-signal rate limit.

## Rejected alternatives

- **Naming endpoints by ID for run members.** Preview copies create their own endpoints, so their IDs would never compare.
- **A separate realtime evidence table.** Realtime calls share every column and rule of the workflow platform calls.
