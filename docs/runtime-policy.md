# Runtime policy convergence

Changing app settings or deployment traffic weights updates control-plane
state; it does not create an application deployment or rebuild an image.
Gateways receive a low-latency notification and independently replay the
durable change log if a notification is missed. Traffic-weight replay loads
live targets before publishing the new weights.

Check gateway application with:

```http
GET /v1/apps/{slug}/policy/status
GET /v1/apps/{slug}/policy/status?wait=10s
```

The response includes a `desired_revision`, `state` (`active`, `pending`, or
`unverified`), and serving/applied/pending/stale gateway counts. `active`
means every registered serving gateway has reported a fresh applied position
at or beyond that revision. A bounded wait may still return `pending` when
its timeout expires. `unverified` means no app change revision or serving
gateway fleet is observable; it never means active.

The `coverage` field currently contains `gateway_app_cache` and
`deployment_traffic`. This status attests gateway cache invalidation and
traffic weights only. It does **not** claim that scheduler scaling, live VM
network/egress policy, guest configuration, cache purges, or other edge-rule
types have converged. Those consumers need their own applied observations
before they can join the same status contract.
