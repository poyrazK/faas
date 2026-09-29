# FaasTenantEgressAbuse / FaasAccountAbuseHold

schedd destroyed an instance because its guest reached an egress abuse ceiling
within one minute (ADR-361). The `reason` label says which signal tripped:

- `fanout` (decision 6): at least the plan's `EgressNewDestinationsPerMinute`
  new destination addresses. That is the pattern of port scanning or
  credential spraying.
- `flood` (decision 9): at least `EgressFloodDropsPerMinute` new flows dropped
  because a single destination received more than
  `EgressNewConnPerDestPerSecond`. That is an HTTP or SYN flood against one
  target.

Both are sent from the node's shared egress address. Upstream
providers act on the address, and the production project was suspended on
2026-09-24 for traffic like this.

A second recycle on the same account within an hour places an **account
abuse hold** (`FaasAccountAbuseHold`). A held account runs nothing: its apps
park, its instances are destroyed without a snapshot, and every wake, job,
execution, cron and deploy is refused with `account_abuse_hold` until an
operator releases it. The customer sees the hold on `GET /v1/account`.

## Symptom

`increase(schedd_egress_abuse_recycles_total{app="…"}[15m]) > 0`. schedd logs
`egress abuse: recycled instance` with the instance, app, node, signal,
observed rate and limit. The instance's `events` row has kind `egress_fanout`
or `egress_flood`.

## Check

1. How hard is it hitting? For fanout, look at
   `rate(vmmd_egress_new_destinations_total{app="…"}[5m]) * 60`. For flood, look
   at `rate(vmmd_egress_denied_total{app="…",class="flood"}[5m]) * 60`. Compare
   either with the plan ceiling in `pkg/api/limits.go`.
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
   contacted in the last 10 minutes, and `... egress_dst_rate` shows the
   addresses it has been flooding. `conntrack -L` inside the netns shows
   the live flows.

## Recover

The hold is operator-only. The same commands can place one by hand, for
example on a provider abuse report:

```
gregale admin abuse-hold place --note "<why>" <account_uuid>
gregale admin abuse-hold release --note "<review outcome>" <account_uuid>
```

Both are audited (`account.abuse_hold_placed` / `account.abuse_hold_released`);
schedd's own holds (reason `egress_fanout` or `egress_flood`) are audited as `accounts.abuse_hold`. After a release the
apps stay parked and wake on their next request.

1. Legitimate fan-out, such as a crawler or a webhook fan-out service: the
   ceiling is per plan. Scale has the highest one. There is no per-app
   override today; raising a plan ceiling means changing `pkg/api/limits.go`,
   which needs an ADR.
   Release the hold once the customer confirms.
2. Abuse or a compromised app: keep the hold. If it has not triggered yet,
   place it by hand, then follow [tenant-abuse.md](tenant-abuse.md) §Recover.
   Keep the destination list from step 4 of Check for the provider's abuse
   desk.
3. The alerts clear 15 minutes after the last recycle or hold.
