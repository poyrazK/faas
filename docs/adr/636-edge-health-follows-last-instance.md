# ADR-636 · The edge health answer follows the app's last instance

- **Status:** proposed
- **Date:** 2026-10-07
- **Amends:** #1639 (monitor-aware edge `/healthz`). The answer still never
  wakes the app, and `health_path_wakes` still sends the probe to the origin.
- **Decision:** when an app has no routable instance, gatewayd-internal answers
  its edge health path from the state of the app's most recently started
  instance.
  - **Healthy:** the last instance is in any state except FAILED (parked,
    stopped, or still waking). The next request wakes it.
  - **Unhealthy:** the last instance is FAILED. The answer is
    `503 app_health_unavailable` with `X-Faas-Health-Reason:
    last_instance_failed`.
  - **Fallback:** the gateway's own wake observation is used only when the
    app never had an instance or the lookup fails. With no observation either,
    the answer stays `503` with reason `unknown`.
  - **Load:** each gateway caches the outcome per app for 15 s, with a 2 s
    query deadline, so the database sees at most one read per app per gateway
    in each window.
- **Why:** H5-6. #1639 kept the last wake outcome in process memory and
  deliberately failed closed when it had none. In practice that meant false
  outages:
  - after every gatewayd-internal restart (every rollout), until real traffic
    woke each app;
  - on any compute gateway that did not run the app's last wake. A monitor's
    probes alternate between gateways, so it flapped.
  - Uptime monitors on scale-to-zero apps saw outages that never happened.

  A local observation is also stale on the other side: a wake that failed on
  this gateway days ago hid later successful wakes run elsewhere. The instance
  row is written by schedd for every wake, on any node, and every gateway can
  read it.
- **Consequences:**
  - A wake that fails before an instance row exists, such as a refused
    admission or a lack of capacity, no longer shows as unhealthy. These are
    platform capacity events, not app health; they still return their own
    errors to real requests.
  - A wake failure reaches the edge answer within 15 s on every gateway.
- **Rejected alternatives:**
  - Wake the app on the first probe after a restart. Monitors would then
    trigger billed running seconds after every rollout.
  - Persist each gateway's local outcome to disk. That survives a restart but
    not the cross-gateway disagreement.
