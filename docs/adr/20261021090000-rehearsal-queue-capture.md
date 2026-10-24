# ADR-20261021090000 · Rehearsal queue capture

- **Status:** accepted
- **Date:** 2026-10-21
- **Amends:** [ADR-20261017201005](20261017201005-rehearsal-network-containment.md), [ADR-20261020090000](20261020090000-rehearsal-event-capture.md)
- **Decision:** A contained rehearsal guest sends queue messages and creates delayed tasks through a run-local platform API. The platform records each request as run evidence and never enqueues it. The sealed action snapshot carries these requests, and comparisons compare them.
- **Why:**
  - Queue sends and delayed tasks go to the public API with an API key. Containment blocks that API, so a contained candidate's queue work failed, and the failure could not be told apart from a regression.
  - An app that sent one message where it used to send two still passed.

## Decision

### Run-local platform API

- **Endpoint.** The guest service listener (TCP 10081 on the tenant bridge) serves `POST /v1/apps/{slug}/queues/send` and `POST /v1/apps/{slug}/delayed-tasks` at the host `api.svc.gregale`.
  - `api` is a reserved app slug, so no customer service can hold this name.
  - Contained DNS already answers every `*.svc.gregale` name with the bridge address.
- **Who it serves.** Only a caller whose app has a contained rehearsal policy. Every other request reaches the service proxy unchanged, including any request from an ordinary or capture-only caller.
- **Identity.** The caller is identified by source address, through the same resolver the service proxy uses. Request headers are never trusted, and the customer's API key is neither checked nor stored.
- **Guest configuration.** schedd stamps `GREGALE_API_URL=http://api.svc.gregale:10081` into the API env of every contained boot and every contained command.
  - The entry follows the customer's plaintext entries, so it takes their place.
  - A customer secret with the same name still wins in guest-init. Calls to that URL stay blocked by containment.
  - A snapshot keeps the value it was booted with. Every snapshot of a contained app was booted inside its run.
- **Writer.** gatewayd-internal writes `scenario_captured_queue_messages`, as it already writes rehearsal async admissions. apid, schedd and vmmd do not write it.

### Validation and capture

- **Same contract as production.**
  - Bodies decode strictly: unknown fields and trailing data are refused.
  - Delayed tasks need exactly one of `delay_seconds` and `scheduled_at`, within the public API's range.
  - The target must be either a member of the same run or an app of the caller's account. Anything else is a 404, as in production.
- **Responses.** On success, the response is `201` with the public API's response shape and `X-Gregale-Rehearsal: captured`. A delayed task reports state `pending` and its computed `scheduled_at`.
- **Storage.** `scenario_captured_queue_messages` holds one row per request:

  | Column | Content |
  |---|---|
  | `kind` | `queue` or `delayed_task` |
  | `target` | `workload:<name>` for a run member, otherwise `app:<slug>` |
  | `queue_name` | The requested queue |
  | `schedule` | `delay:<seconds>` or `absolute`. A wall-clock instant is never stored for comparison. |
  | `message` | The decoded request without its schedule fields |

  Rows cascade with the policy.
- **Admission.** Capture takes the run's advisory lock, the same lock the seal takes. A request is therefore either in the sealed snapshot or refused as late (`409 rehearsal_actions_sealed`).
- **Bounds.**
  - 256 requests per run; one extra row marks overflow.
  - 64 KiB per request body. A larger body is kept only as an oversized marker, and the guest still receives `201`.
  - Overflow or an oversized body makes the snapshot `incomplete`.

### Snapshot and comparison

- **Snapshot fields.** These are additive: `queue_capture_version` (1), `queue_messages` and `incomplete_queue_messages`.
- **Comparison.** Requests compare as a multiset per workload, kind, target and queue, by schedule and canonical message.
  - A duplicate send fails the comparison, as does a changed delay or a changed payload.
  - Platform IDs never count.
  - A comparison across different queue capture versions is inconclusive.

## Consequences

- **Not executed.** Captured messages are never delivered, even to a member of the same run. A consumer's behaviour must be driven from the recipe, as for crons (ADR-20261020200000).
  - Executing same-run deliveries needs a run-scoped queue. That work must also cover queue sources in the managed-work journal and claim fences, delayed rows that outlive the run, and preview queue bindings. It is a separate decision.
- **Capture-only rehearsals are unchanged.** Their apps keep their ordinary network and API key, so their queue sends still reach real queues. Use containment when queue work must stay inside the run.
- **Org-scoped routes.** `/v1/orgs/{org}/apps/{slug}/...` and other public API routes are not served at `api.svc.gregale`. They reach the service proxy, which returns 404.
- **Cost.** A non-matching request on the guest listener costs a string check. A matching request from an ordinary caller costs one caller lookup and one primary-key read of the policy.

## Rejected alternatives

- **The outbound relay (`GREGALE_OUTBOUND_URL`).** It reaches the rehearsal broker only for apps with simulation fixtures. Apps would also need different code in production.
- **Answering `api.gregale.dev` inside the run.** HTTPS to the public name cannot terminate without a certificate the platform must not mint for guests.
- **Executing every send in a run-scoped queue now.** See Consequences: the journal, claim and retention changes need their own decision.
