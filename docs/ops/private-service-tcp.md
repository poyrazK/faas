# Private TCP service addresses rollout

Private TCP service addressing ([ADR-576](../adr/576-private-tcp-service-addressing.md))
lets a workload reach a same-account service's declared TCP listener on its
natural port, for example `redis://cache.svc.gregale:6379`. It is off by
default. Three switches must be turned on together on every compute host.

## Switches

```yaml
# host_vars/<compute-host>.yml
faas_service_tcp_enabled: true          # nftables role: boot-time host NAT
faas_service_tcp_https_enabled: false   # true only where gatewayd_service_proxy_https_listen is set
gatewayd_service_tcp_listen: "10.100.0.1:10082"   # same bridge address as gatewayd_service_proxy_listen
```

vmmd reads `service_tcp_enabled = true` in `[compute_node]`, or
`FAAS_SERVICE_TCP_ENABLED=true`. vmmd owns the live host ruleset and
re-renders it after every VM change. The nftables role only keeps the
boot-time ruleset identical until vmmd starts. vmmd turns service-address
`:443` forwarding on only when its private service CA is configured. Keep
`faas_service_tcp_https_enabled` in step with that.

## Order

1. Apply the migrations. These add `apps.service_address_index` and
   `compute_nodes.service_address_ready_at`.
2. Roll the compute hosts with all three switches on. When vmmd boots, it
   logs `service address readiness recorded` with a `ready_at` stamp.
3. Set `gatewayd_service_tcp_dns: true` (`service_tcp_dns = true`). This is
   the customer-visible switch: `<service>.svc.gregale` now answers with the
   target's service address. Until then, no workload is handed one.

DNS answers a service address only when all of these hold:

- the querying source maps to one live instance on the node;
- that instance row was created at or after the node's
  `service_address_ready_at`;
- the name resolves to a live app in the caller's account.

In every other case, including any lookup error, it keeps the tenant-bridge
answer. That answer always serves HTTP. HTTP calls to a service address are
forwarded by the host to the same HTTP service proxy, so existing
`GREGALE_SERVICE_*_URL` bindings keep working.

To roll back, turn off DNS answers first, then the listener, then vmmd and
the nftables role. Never run a vmmd build without this feature on a host
whose `service_address_ready_at` is set.

## Verify

The gatewayd-internal log contains `private service TCP listening`. The host
ruleset contains the service NAT:

```bash
sudo nft list chain inet faas prerouting
```

Metrics on the gatewayd-internal `/metrics` endpoint:

- `gatewayd_internal_service_tcp_sessions_accepted_total`
- `gatewayd_internal_service_tcp_sessions_rejected_total{reason}`
- `gatewayd_internal_service_tcp_sessions_completed_total{outcome}`
- `gatewayd_internal_service_tcp_active_sessions` and `_by_account`
- `gatewayd_internal_service_tcp_bytes_total{direction}`
- `gatewayd_internal_service_tcp_idle_timeouts_total`

The rejection reasons are a closed set:

| Reason | Meaning |
|---|---|
| `not_service_address` | The connection was not DNATed from the service block. |
| `reserved_port` | The guest dialed 10080, 10081 or 443, which belong to the HTTP mesh. |
| `unknown_caller` | The source address does not map to a live instance on this node. |
| `identity_unavailable` | The source address could not be checked against instance state. |
| `unknown_service` | No app in the caller's account holds the address. |
| `target_unavailable` | The target is in maintenance, security quarantine, or a held, suspended or deleted account. |
| `denied`, `binding_denied`, `caller_denied`, `preview_denied` | Service policy denied the call, as on the HTTP mesh. |
| `authorization_unavailable` | The service policy check itself failed. |
| `call_scope` | The target grants this caller only method/path scopes, which raw TCP cannot honour. |
| `undeclared_port` | The target did not declare that TCP port. |
| `account_limit` | `ServiceTCPSessionsPerAccount` was reached for the account's plan on this node. |
| `global_limit` | `ServiceTCPSessionsPerNodeMax` was reached on this node. |
| `release` | The caller's project release graph could not route the call. |
| `wake_queue_full`, `wake_failed`, `no_replica` | A parked target could not be woken within 30 s. |
| `guest_unreachable` | Neither attempted replica accepted the dial. |
| `registry_unavailable`, `original_destination` | A platform fault. |

Qualification needs a native metal run of `TestMetalServiceAddress*`
(`pkg/netns`). That run proves the netns admission, the host NAT, the
`SO_ORIGINAL_DST` lookup and the `:443` boundary on a real kernel.
