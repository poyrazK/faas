# Active-passive HA runbook (Tier A8 / ADR-083)

This runbook is the operator-facing counterpart to
`docs/adr/083-active-passive-ha-topology.md`. It closes the
§14 M8 row "Gate-A runbook (2nd box active-passive)" and
covers the day-2 operations of an active-passive topology on
the Gregale multi-box fleet.

## Pre-flight

Before promoting a fleet to active-passive HA, verify:

1. **Tier A4 + A5 + A7 are shipped.** The runbook assumes
   `cmd/schedd` has the per-node schedd + parked-app
   rebalance (ADR-064) wired in, the cross-node live-instance
   migration (ADR-066) is operational, and `gatewayd-public`
   is the TLS-only edge (ADR-070). Without these, the
   active-passive topology is half-built — the leader election
   runs but no traffic shifts.
2. **`compute_nodes` has ≥ 2 active rows.** A single-box
   fleet has no failover surface; `LexMin` returns one winner
   and the topology degenerates to single-leader.
3. **DNS provider is reachable.** Verify via the smoke test
   in `Validation matrix` step 1 below.
4. **The active-passive HA quotas are wired.** Confirm the
   two limits in `pkg/api/limits.go`:
   - `HAFailoverProbeTimeoutMS = 500`
   - `HADNSRecordStaleSeconds = 30`

## Procedure (operator-driven drain)

The active-passive flip is initiated by the operator's
standard drain command (the same one Tier A4 + A5 use):

```sql
UPDATE compute_nodes
   SET active = false
 WHERE name = '<dying-leader>';
```

The downstream behaviour is automatic:

1. **pg_notify fires** on the `compute_node_changed` channel
   (migration `00026`).
2. **Every active peer's schedd re-elects.** Each
   `gatewayd-public` runs `pkg/gateway/leader.ElectLeader`
   (lex-min over `compute_nodes.name WHERE active = true`).
   The new leader is the lex-min survivor.
3. **Old leader drains.** The dying leader's
   `cmd/gatewayd-public/dns_handoff.go::DNSHandoff.Run` runs:
   - `StandbyState → draining` (the alert rule
     `FaasStandbyStateDraining` may fire on an idle box).
   - Wait for in-flight requests to drain, bounded by
     `HADNSRecordStaleSeconds = 30 s`.
   - `dns.DeleteRecord(oldLeader.name)`.
   - `gateway_active_passive_failovers_total{outcome="dns_flipped"}` += 1.
4. **New leader writes DNS.** The new leader's
   `ElectLeader` callback fires
   `dns.UpsertRecord(newLeader.name, newLeader.egressIP)`.
5. **Standbys pre-warm caches.** Each standby's
   `cmd/gatewayd-public/standby_warmup.go::WarmupLoop.Run`
   issues HTTP HEAD probes against `cmd/gatewayd-internal`
   on each app's hostname, bounded by
   `HAFailoverProbeTimeoutMS = 500 ms` per probe.

End-to-end, the flip completes inside DNS TTL (10–30 s).
Customer traffic sees the new leader after the resolver's
TTL window elapses.

## Validation matrix

The runbook's success gates mirror §14 M8 + the §12
dashboard:

| Gate | Metric / Signal | Operator command |
|---|---|---|
| Leader is elected | `<prefix>_gateway_standby_state == 2` on exactly one node | `curl -s http://node-X:9092/metrics \| grep standby_state` |
| Standbys are warm | All other nodes show `standby_state == 2` | `curl -s http://node-X:9092/metrics \| grep standby_state` for each peer |
| DNS flips on drain | `<prefix>_gateway_active_passive_failovers_total{outcome="dns_flipped"} >= 1` on new leader | `curl -s http://new-leader:9092/metrics \| grep active_passive_failovers_total` |
| No 5xx during flip | Customer app's error rate stays ≤ baseline | `curl -s https://<app>.example.com/healthz` over the drain window |
| `StandbyState → warming → warm` completes ≤ 60 s | `FaasStandbyStateWarmingTooLong` does NOT fire | `curl -s http://node-X:9092/metrics \| grep standby_state` |

## Rollback

If the active-passive flip goes wrong (DNS stale, peer
unreachable, stuck drain):

1. **Restore the dying leader's DNS record by hand:**

   ```sh
   curl -X POST "https://api.cloudflare.com/client/v4/zones/${CF_ZONE_ID}/dns_records" \
     -H "Authorization: Bearer ${CF_API_TOKEN}" \
     -H "Content-Type: application/json" \
     -d "{\"type\":\"A\",\"name\":\"<old-leader>.example.com\",\"content\":\"<OLD_IP>\",\"ttl\":60,\"proxied\":false}"
   ```

   (Cloudflare API v4 — `proxied:false` matches the production
   `pkg/gateway/dns_provider_cloudflare.go` shape. Caddy terminates
   TLS at the edge, not Cloudflare, so the A-record must NOT be
   proxied; otherwise Caddy's `X-Forwarded-For` sees Cloudflare's
   anycast IPs instead of the customer's resolver.)

2. **Set `active=true` on the dying leader:**

   ```sql
   UPDATE compute_nodes
      SET active = true
    WHERE name = '<old-leader>';
   ```

3. **Verify the leader flipped back:**

   ```sh
   psql -c "select name, active from compute_nodes order by name;"
   ```

   The lex-min over active rows should now include the old
   leader.

4. **Inspect the metric counter** to understand the failure
   mode:

   - `outcome="dns_stale"` → DNS provider unreachable; check
     Cloudflare status page (`https://www.cloudflarestatus.com`)
     and the token's permission set (Zone:DNS:Edit on the zone
     in question).
   - `outcome="peer_unreachable"` → pg_notify consumer
     fell behind; check schedd logs for dropped events.
   - `outcome="manual_drain"` → operator-initiated path;
     no action needed.

5. **Page the on-call** if the failure mode is unclear;
   the escalation section below covers this.

## Switching DNS providers

The default DNS provider is `cloudflare` (production, paired with
Caddy upstream of `gatewayd-public`). On staging or during a drill,
operators may set `FAAS_DNS_PROVIDER=manual` to disable real DNS
writes — the manual provider prints the curl to stderr so an
operator can flip DNS by hand, but it does not change the
leader-election topology.

To switch from `manual` (drill mode) to `cloudflare` (prod mode):

1. **Generate a Cloudflare API token** with Zone:DNS:Edit scope
   on the target zone (`api.gregale.dev`). Never use the global
   API key — the token-scoped key is what the secretbox namespace
   expects.
2. **Seal the token** with `pkg/secretbox.SealBytes`:

   ```sh
   faas secrets seal --namespace=dns_provider --file=cf-token.txt
   ```

   The output is the `FAAS_DNS_PROVIDER_SEALED` env var value
   (`pkg/secretbox` writes a base64 blob with the namespace
   prefix-on-blob layout, see pkg/secretbox/seal.go).
3. **Restart both `gatewayd-public` daemons** with:

   ```sh
   FAAS_DNS_PROVIDER=cloudflare \
   FAAS_DNS_ZONE=example.com \
   FAAS_DNS_PROVIDER_SEALED=<sealed-blob> \
   FAAS_HOST_KEY_PATH=/etc/faas/secrets/host.age \
     systemctl restart gatewayd-public
   ```

   The `FAAS_HOST_KEY_PATH` env var is the existing
   `pkg/secretbox.LoadHostKeys` precedent — see
   `cmd/gatewayd-internal/run.go:395`. Empty → the unseal helper
   returns `errSecretBoxUnconfigured` at the first DNS attempt,
   which surfaces as `outcome="dns_stale"` on the failover
   counter (the right behavior: fail loud, never silent no-op).
4. **Verify the unseal path** by running the drill script
   (`deploy/lima/run-ha-failover.sh`) on the Lima fleet and
   confirming `outcome="dns_flipped"` advances (not
   `outcome="manual_drain"`).

## Escalation

The Tier A8 escalation tree (in order of preference):

1. **DNS provider reachable but failing writes.** The
   Cloudflare API may be degraded; the runbook's manual curl
   above restores the record by hand. The metric surfaces
   `outcome="dns_stale"` so the on-call sees the failure
   without needing to grep logs.
2. **Peer schedd falling behind on pg_notify.** Tier A4 +
   A5 already use the same pg_notify channel; a peer-slow
   issue is most likely an existing A4 / A5 problem
   surfacing through A8. Inspect
   `pkg/sched/router_watcher.go` and
   `pkg/sched/rebalancer.go` log lines.
3. **Standby warming stuck at `warming`.** The
   `FaasStandbyStateWarmingTooLong` alert fires at 60 s.
   Inspect the standby's warmup scraper:
   `journalctl -u gatewayd-public | grep warmup`.
4. **All paths exhausted.** Page the platform team via the
   existing `faas-incident` PagerDuty service.

## References

- ADR-083 (this runbook's source):
  `docs/adr/083-active-passive-ha-topology.md`
- ADR-070 (Tier A7 edge split, prerequisite):
  `docs/adr/070-tier-a7-edge-split.md`
- ADR-066 (Tier A5 live-instance migration, prerequisite):
  `docs/adr/066-tier-a5-cross-node-live-migration.md`
- ADR-064 (Tier A4 parked-app rebalance, prerequisite):
  `docs/adr/064-tier-a4-cross-node-app-rebalance.md`
- Spec §14 M8 row:
  `docs/faas_implementation_spec.md`
- Issue #297 (umbrella)
- Two-node Lima fleet: `deploy/lima/faas-metal-2node-ha.yaml`

## Acceptance

The runbook is closed when ALL of the following pass on the
two-node Lima fleet (`make ha-failover-drill`):

- [ ] Drain event fires (`UPDATE compute_nodes SET
      active=false WHERE name='node-a'`).
- [ ] Within `HADNSRecordStaleSeconds = 30 s`:
      - `gateway_active_passive_failovers_total{outcome="dns_flipped"} >= 1` on the new leader.
      - The old leader's `gateway_standby_state` shows
        `draining` (3).
      - Customer app's `curl` returns 200 OK via the new
        leader's DNS-resolved name.
- [ ] `make leakcheck` reports zero leaked netns/TAPs/cgroups
      after the drill.
- [ ] `tests/property/concurrency_test.go` passes — under
      random `compute_node_changed` events,
      `pkg/gateway/leader.ElectLeader` always returns
      exactly one leader.
