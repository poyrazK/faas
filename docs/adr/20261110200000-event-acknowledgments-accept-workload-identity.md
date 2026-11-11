# ADR-20261110200000 · Deferred event acknowledgments accept workload identity

Status: accepted

## Context

A deferred-ack consumer (ADR-1394, ADR-1409) renews and completes its delivery
through `/v1/apps/{slug}/event-deliveries/{invocationID}/acknowledgment/*`.
Those routes took only the API-key chain, so writes needed a `deploy:write`
key inside the worker. That key can also deploy, change secrets and delete
the app: far more authority than settling one delivery needs, and it is a
long-lived secret copied into the image or env. Production testing
showed every deferred-ack worker had to carry one. Queue leases
already solved the same problem with workload identity (ADR-1140, ADR-1141).

## Decision

The five acknowledgment routes (GET status; POST renew, complete, retry,
dead-letter) also accept a workload token for the new audience
`gregale:events`. A JWT-shaped bearer takes the workload path; every other
credential keeps the API-key chain and its scopes unchanged.

A workload token is accepted only when it verifies under the cluster workload
trust for `gregale:events`, names the app in the path (same account, not
deleted, org slug matching on org routes), the app's resource account is
active, and its instance belongs to that app and counts for concurrency. The
account's capability gate applies as on the key chain.

Unlike queue leases, stage deployments are not refused: event subscriptions
deliver to stage deployments too, and a stage worker must settle its own
deliveries. The delivery token stays the fence on each call, so a live
instance of the app can only settle deliveries whose token it was handed.

Tokens for other audiences (`gregale:queues`, `gregale:flags`, …) are refused,
and a `gregale:events` token is refused by every other Gregale API. The guest
endpoint already mints any audience, so no guest or vmmd change is needed.

## Consequences

Workers drop their `deploy:write` key: `GET
$FAAS_WORKLOAD_IDENTITY_ENDPOINT?audience=gregale:events` gives a five-minute
token. Existing key-based workers keep working. A node without workload trust
configured refuses workload tokens with 401, and the worker falls back to a
key. SDK helpers that fetch the token are follow-up work.
