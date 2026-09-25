# ADR-247: Revision-scoped request concurrency cap

- **Status:** Accepted
- **Decision:** Let a deployment opt into a hard per-instance cap on simultaneous inbound requests. Omission inherits the account plan's existing `concurrency_per_vm` limit. Explicit values are positive and may only lower that plan limit.

## Context

Gregale currently enforces one plan-derived request limit at the gateway for every instance, regardless of which live deployment receives traffic. A workload that needs lower request fan-out—for example, because each request consumes substantial memory or holds a scarce downstream connection—cannot express that safety boundary without changing the app's plan-level behavior.

This setting is different from the `concurrent_requests` autoscaling target. The autoscaling target asks the scheduler when to add capacity; this cap is an admission gate that prevents one instance from serving more than the configured number of requests at once.

## Decision

- Store an optional `max_concurrent_requests` on each immutable deployment. `NULL` means the deployment inherits the plan's `concurrency_per_vm` limit.
- Accept explicit values from 1 through the plan's advertised per-instance limit. The override only lowers the existing plan cap; it does not raise tier entitlements.
- Resolve the cap by the selected target's deployment ID in the gateway. Apply it to the initial target and every alternate candidate considered while a request waits for capacity.
- Keep the app-wide instance ceiling and existing scale-up signals independent. A lower request cap can cause the gateway's bounded burst admission to seek more instances, but it does not alter the app-wide ceiling.
- Preserve the value when retrying a deployment and expose an explicitly configured value in deployment detail responses. The setting is not mutable after creation.

## Consequences

Customers can tune request fan-out safely per rollout revision, including canary traffic, without coupling stable and candidate deployments. Values that exceed the plan limit fail validation, and older deployments continue to inherit exactly today's limit.
