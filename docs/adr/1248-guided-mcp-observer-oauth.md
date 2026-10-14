# ADR-1248: Guided OAuth connection for managed MCP observers

## Context

Managed observers renew OAuth grants, but customers previously obtained the
initial refresh grant outside Gregale and stored it as an app environment secret.
A guided connection must bind browser consent to the exact owning app and role,
without returning refresh tokens or requiring a public callback service.

## Decision

The CLI binds an IPv4 loopback HTTP listener and opens the explicitly configured
HTTPS authorization endpoint. The control plane generates cryptographic state
and an S256 PKCE verifier, seals the verifier and exact loopback redirect URI,
and retains only the state hash in the role's durable monitor payload. Flows
expire within five minutes. Begin retires the old revision and observer lease.

The CLI checks callback host, path and state before forwarding the code in an
authenticated POST. The control plane atomically consumes the matching state
before token exchange; replay, expiry, account/role mismatch and superseded
revisions fail closed. An exchange failure requires a new connection attempt.
The provider must support registered IPv4 loopback redirects with dynamic ports
and PKCE; no dynamic client registration or discovery-directed endpoints are used.
Public clients send client_id; confidential clients use client_secret_basic.

Provider calls use guarded public egress without environment proxies or
redirects, bounded response sizes and deadlines. Tokens are sealed directly in
the owning role connection, never returned or written to guest app secrets.
Finish checks the consumed flow and role revision again, saves credentials and
retires active observer leases. This prevents an in-flight observer from replacing
a newly authorized connection. Granted scopes are retained, and a response may
narrow but cannot broaden explicitly requested scopes. If no scopes were requested,
provider defaults are accepted and recorded. Renewal retains or narrows scope
metadata under the same rule.

Connect/reconnect and completion require deploy intent, MFA and secrets:read or
admin. Disconnect requires deploy intent and MFA; it remains possible after plan
or workload changes and removes retained tokens and pending authorization state.
It invalidates active leases and catalog evidence. The configured role remains
configured, so an enabled disconnected observer reports credential unavailable;
customers can pause it to exclude it from required health.

## Consequences

An initial refresh secret is no longer necessary for guided connections, and
rotated tokens use the existing durable renewal worker. Manual refresh-secret
connections remain supported. Loopback callbacks avoid public unauthenticated
control-plane routes; remote/headless environments must provide a browser that
can reach the CLI's loopback listener. The CLI closes its listener on completion,
timeout or interruption and never prints authorization codes or token values.

Provider exchanges and database commits cannot be atomic. A provider may issue
or rotate a grant before a crash or failed fenced commit; reconnect is the recovery
path. Live provider compatibility still requires provider acceptance evidence.
