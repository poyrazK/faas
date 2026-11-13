# ADR-20261107090000: Preview markers reach the target service; removing a Compose service's ports makes it internal

- Status: accepted
- Date: 2026-11-07
- Amends: ADR-206 (service caller identity), ADR-20261029120000 (Compose
  visibility)

## Context

A call from a PR preview carries `X-Faas-Caller-Env: preview` and
`X-Faas-Caller-Preview-Of: <app>`, and the networking guide tells services to
use them to skip irreversible work or refuse the call. The service proxy set
them, but the guest boundary (`forwardproxy.go`) drops every `x-faas-*`
header outside an allowlist, and the markers were not on it. No service ever
received them, so production services that accept preview calls (the
default) could not tell preview traffic from production traffic. The only
test checked the service proxy's outbound request, not what the guest got.

A Compose service's visibility was set only when its app was created.
Removing `ports:`, or binding them to a loopback address, which is how a
Compose file makes a service internal, left an existing app public with no
word in the plan or on a GitHub check.

## Decision

1. The service proxy marks the request context when it sets the preview
   markers, as it does for the signed caller assertion. The guest boundary
   lets the two marker headers through only on a marked request; the same
   names from a client or a workload stay stripped. `X-Faas-Caller-App`
   stays on the node-local hop.
2. The guide no longer says a service learns its caller from platform
   headers by default: it does not; signed caller assertions are the way.
3. A production project deploy makes internal each existing app whose Compose
   service publishes no ports, notifies the gateways and audits
   `app.visibility_changed` with `source: compose`. The plan states it. The
   reverse (a service that starts publishing ports) is never applied
   automatically; the plan says how to publish it. A named-environment deploy
   changes no visibility, because visibility is app-wide, and says so.

## Consequences

- A service that relied on staying public after its ports were removed loses
  its public URL on the next production deploy; the plan names it.
- GitHub pushes still leave visibility unchanged (ADR-20261103112845: pushes
  never take reviewed-only actions).

## Alternatives rejected

- Applying both directions: making a service public from a Compose edit is an
  exposure a reviewer should choose explicitly.
- Sending `X-Faas-Caller-App` to the guest: it is unsigned; signed assertions
  are the verifiable form.
