# FaasMeterdFleetEgressStreamsMissing

This alert means meterd discovered more active compute nodes than it currently
has authenticated gateway usage streams. Customer requests may still succeed;
the affected gateway keeps unacknowledged request, response-byte, and cold-boot
frames in `/var/lib/faas/egress-meter/pending.json` until meterd commits them.

## Symptom

`meterd_fleet_egress_expected_streams` is greater than
`meterd_fleet_egress_connected_streams`, and the alert remains active after the
normal reconnect window.

## Check

1. Compare `meterd_fleet_egress_expected_streams` with
   `meterd_fleet_egress_connected_streams` and inspect
   `meterd_fleet_egress_failures_total` by `stage`.
2. On the control plane, inspect recent `faas-meterd` logs for discovery, TLS,
   persistence, or ACK failures. Confirm PostgreSQL is writable before
   restarting meterd; committed frames are deduplicated on replay.
3. For each active compute node, verify its `gateway_target_url`, TCP reachability
   on port 9092, the `egress.faas` server certificate, and
   `faas-gatewayd-internal.service` readiness.

## Recover

4. Do not delete the compute-local pending file. If it is corrupt, preserve a
   copy for billing reconciliation and take the node out of service before
   repairing it.
5. The alert clears after every active node reconnects. Confirm the expected and
   connected gauges match and that persistence failures stop increasing.
