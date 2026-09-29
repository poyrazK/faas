# FaasTenantEgressFanout

schedd destroyed an instance because its guest contacted at least its plan's
`EgressNewDestinationsPerMinute` new destination addresses within one minute
(ADR-361 decision 6). That is the pattern of port scanning, credential
spraying or a flood, all sent from the node's shared egress address. Upstream
providers act on the address, and the production project was suspended on
2026-09-24 for traffic like this.

## Symptom

`increase(schedd_egress_fanout_recycles_total{app="…"}[15m]) > 0`. schedd logs
`egress fan-out: recycled instance` with the instance, app, node, observed
rate and limit. The instance's `events` row has kind `egress_fanout`.

## Check

1. How hard is it hitting? Look at
   `rate(vmmd_egress_new_destinations_total{app="…"}[5m]) * 60` next to the
   plan ceiling in `pkg/api/limits.go`.
2. What is it doing? Look at the blocked attempts in
   `vmmd_egress_denied_total{app="…"}` by `class`:
   - `port_policy`: undeclared ports or non-TCP;
   - `rate_limit`: new-flow rate exceeded;
   - `rfc1918` / `metadata`: lateral-movement attempts.
3. Is it doing it again? After a recycle the next request starts a clean
   instance. Repeated recycles over several windows mean the behaviour comes
   from the app itself, not a one-off compromise.
4. Where is it going? On the compute node, `ip netns exec <instance netns> nft
   list set ip faas egress_dsts` shows the addresses the instance has
   contacted in the last 10 minutes. `conntrack -L` inside the netns shows
   the live flows.

## Recover

1. Legitimate fan-out, such as a crawler or a webhook fan-out service: the
   ceiling is per plan. Scale has the highest one. There is no per-app
   override today; raising a plan ceiling means changing `pkg/api/limits.go`,
   which needs an ADR.
2. Abuse or a compromised app: park the app now, then suspend the account and
   follow [tenant-abuse.md](tenant-abuse.md) §Recover. Keep the destination
   list from step 4 of Check for the provider's abuse desk.
3. The alert clears 15 minutes after the last recycle.
