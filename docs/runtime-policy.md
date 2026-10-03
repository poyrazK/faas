# Runtime policy convergence

Gateway app-cache, deployment-traffic, edge-rule, CORS-preset, and app egress
allowlist changes update runtime policy without creating an application
deployment or rebuilding an image. Gateways receive low-latency notifications
and independently replay durable change logs if a notification is missed.
The gateway request envelope—such as the app request timeout, concurrency
limits, and app-wide rate-limit RPS/burst overrides—is stored on the app row
and refreshed through that same durable app-change path. Rate overrides can
only tighten the plan defaults; setting either value to zero restores that
dimension to its plan default. The `request_policy` status is app-scoped and
filters the app-row revision from the combined app/traffic cursor, so traffic
rollout lag does not obscure an already-applied request policy. A gateway
acknowledges the app change after invalidating its cached projection;
subsequent lookups load the current request settings from the app row.
Explicit response-cache purges are durably recorded and replayed by each
serving gateway. A gateway advances its purge watermark only after invalidating
its local cache and the optional shared Redis tier; a Redis failure leaves the
request pending for retry.
Traffic-weight replay loads live targets before publishing the new weights.
Schedd treats the app row's egress allowlist revision as desired state,
reconciles live app/node pairs at startup and periodically, and records a
per-node acknowledgement only after vmmd's in-place nftables update succeeds.
The revision also covers the app's extra egress ports (ADR-361), so a port
change converges on live instances the same way.
Scaling inputs likewise live on the app row: schedd observes the current
scaling-policy revision after an `app_changed` wake and on a periodic repair
pass. That observation confirms the scheduler loaded the configuration; it
does not claim a metric-driven replica target has already been reached. These
policy updates do not create an application deployment. Notifications are
fast wake-ups, not the source of truth.
The per-app CPU limit is also runtime policy: schedd pushes a new desired quota
to each node hosting the app, and vmmd updates the live Firecracker cgroup
without restarting the guest. For multi-workload VMs, vmmd also updates the
guest's main-workload cgroup over its existing host-to-guest control channel;
serving-node acknowledgements wait for both writes. Sidecar-specific CPU
ceilings remain independent workload policy. Paused warm snapshots receive the
host ceiling immediately and reapply guest policy during restore before
becoming available. Periodic reconciliation repairs missed wakes. RAM size and
vCPU topology are still boot-time VM shape and are not claimed to be hot-applied
by this component.

Check gateway application with:

```http
GET /v1/apps/{slug}/policy/status
GET /v1/apps/{slug}/policy/status?wait=10s
```

The response includes the app-scoped `desired_revision`, `state` (`active`,
`pending`, or `unverified`), and serving/applied/pending/stale gateway counts.
These top-level fields remain the gateway app-cache/traffic projection; read
each named component for its own convergence state.
The `edge_rules` component is also app-scoped; `cors_presets` is explicitly
account-scoped because presets can be shared across the account's apps. For
each component, `active` means every registered serving gateway has reported
a fresh applied position at or beyond that component's revision. A bounded
wait may still return `pending` when its timeout expires. `unverified` means
no corresponding change revision or serving gateway fleet is observable; it
never means active.

The top-level `coverage` field contains `gateway_app_cache`,
`gateway_request_policy`, `deployment_traffic`, `response_cache_purge`,
`app_egress_allowlist`, `app_cpu_limit`, and `scheduler_scaling`; separate components report app
edge rules, account CORS presets, the app-scoped request envelope,
response-cache purge application on every serving gateway, app egress status
and CPU quota status across nodes hosting live instances, and the owning
schedd's observed scaling revision. Egress and CPU report `active` only when
every such node has a fresh acknowledgement at or beyond the app's desired
revision. Scheduler scaling is
`active` when the expected schedd has freshly observed the latest revision;
this is policy observation, not replica-count attainment. A missing or stale
observation is never reported as active.

The contract still does **not** attest every host-level firewall rule or
guest-process configuration. Changes to environment, entrypoint, or memory
topology may require a fresh instance; those remain distinct from the hot
policy components listed here.
