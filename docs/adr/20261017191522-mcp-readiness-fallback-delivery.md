# 1280: Bounded readiness webhook failover and incident routing

Status: Accepted

## Context

A durable readiness outbox survives receiver outages, but a single unavailable
receiver can indefinitely hide an incident. Recovery must reach receivers that
have received its failure, including a primary informed before reminder failover.

## Decision

Add one optional signed fallback per readiness webhook rule. Admin+MFA replaces
its policy with an explicit expected version; reads use read scope+MFA. Validate
and DNS-check public HTTP(S) destinations, seal secrets using the existing alert
namespace, and never return plaintext or ciphertext. Policy defaults disabled.

Migration backfills already attempted incidents to preserve live-primary behavior.
At an incident's first delivery claim, snapshot the enabled fallback policy and
both destinations with sealed credentials. Policy changes apply to incidents not
yet claimed. An incident first claimed without fallback retains existing live
primary behavior and cannot silently acquire fallback later. With fallback enabled,
URLs stay pinned to the incident. A current rule/policy with the identical pinned
URL supplies its latest secret on each claim, allowing credential rotation without
rerouting. Different URLs retain the incident's last used sealed credential; disabling fallback
prevents its use on new incidents, not already pinned incidents. Disable the rule
or abandon its queue head to stop existing deliveries.

Count completed failed primary dispatch claims per logical event, not internal
HTTP retries, crashed leases, or elapsed time. Threshold is 1–10. Once reached,
permanently select fallback for that incident. The next evaluator pass can claim
it immediately. There is no fanout for ordinary fired notifications and no second
fallback. Failed fallback transport uses existing bounded dispatch and cooldown;
logical event IDs and payloads remain immutable across destinations and retries.

Track successful primary/fallback delivery per event and informed destinations per
incident. After failover, deliver recovery to fallback first and also to primary
if it previously accepted a fired notification. Retry only the outstanding target;
complete the logical recovery only when its required targets succeed. A broken
primary can therefore keep a partly delivered recovery at the FIFO head; existing
explicit abandonment remains available. Fallback can receive a standalone recovery
if failover first happens during recovery. An uncertain expired primary lease is
conservatively treated as possibly informed, since a receiver may have accepted it
before the worker crashed; recovery includes that primary.

Claim and completion serialize app→rule→event, fence lease tokens and atomically
persist route progress with outbox state. Maintenance, acknowledgement and stale
reminder suppression run before claims as before; acknowledgement affects reminders
only. Physical delivery history identifies destination, event ID and routing reason.
Timeline and queue views expose credential-free per-destination progress. Migration
rollback refuses to discard routing history. Upgrade all readiness evaluators before
enabling fallback; older workers cannot enforce pinned destination routing.

## Consequences

A primary receiver outage can reach a separately operated receiver without losing
incident identity or queue order. At-least-once delivery still requires receiver
deduplication by logical event ID. Two informed receivers may require two recovery
claims. Receiver addresses remain pinned for active incidents; intentional URL
changes affect later incidents. Native paging integrations and automatic escalation
beyond this single fallback remain separate work.
