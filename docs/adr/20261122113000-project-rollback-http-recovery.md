# ADR-20261122113000 · Observe HTTP recovery for project rollbacks

Project rollback completion still represents the outcome of its checked
rollbacks. The receipt also reports routing commit, gateway application, and the
first confirmed upstream 2xx for each targeted HTTP workload. Project recovery
time spans acceptance through the last of those successful responses.

Each checked HTTP cutover records a unique route generation and the serving
gateway roster in PostgreSQL. Gateways acknowledge the exact restored target
after refreshing their routing caches. The gateway records a successful
unpinned public request to the same target after the acknowledgement barrier.
The worker republishes pending generations so a lost notification or gateway
restart can recover the observation. Until the gateway barrier passes, replay
and acknowledgements reconcile the serving gateway roster. Retired gateways
leave the roster and newly serving gateways must acknowledge the route.
Superseded routes and a 30-minute
observation window stop pending verification.

The receipt reports awaiting traffic when gateways have applied the route and
a qualifying response has yet to arrive. Existing receipts without tracked
HTTP workloads retain their original fields.
