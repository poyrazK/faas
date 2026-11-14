# ADR-20261113173610: Independent Node SDK clients

- **Status:** accepted
- **Date:** 2026-11-13
- **Milestone:** SDK developer and agent integration
- **Related:** ADR-20261023115741 (capability boundaries), ADR-20261112110000 (SDK public repositories)

## Context

The Node SDK's static generated services share `OpenAPI`, and `FaaSClient`
installs retry, error, logging, and idempotency behavior on global fetch.
Creating a second client can redirect another caller's requests or credentials.
Concurrent accounts and agent tasks need independent clients while existing
applications need a gradual migration.

## Decision

Add `createClient(baseURL, options)` with bound generated service namespaces,
such as `client.apps.getApp`. Each factory call owns configuration and a fetch
transport. It neither reads legacy configuration nor writes process globals.
The public factory exposes the public generated services; the preview factory
exposes the full generated service surface.

Generation post-processors add an optional transport to `OpenAPIConfig`, a
fresh defaults factory, and a protected service configuration hook. Bound
methods use a private receiver whose hook returns the client's current config;
static calls keep the existing shared configuration fallback. The patches fail
closed on generator drift and are idempotent.

Updates replace immutable config snapshots. Each operation captures the config
when invoked, so token, endpoint, and default-key changes affect future calls
without changing requests resolving headers or waiting to retry. Shared
transport code preserves retry, cancellation, deadlines, binary decoding,
typed errors, and one key per logical mutation. Client retry options are copied.
The default platform fetch is captured at module load, before the legacy
constructor can install its wrapper. Custom transports remain explicitly injectable.

Keep `FaaSClient` and static services for compatibility; mark the façade
deprecated and document migration. Standalone helpers that use `OpenAPI` keep
their legacy configuration contract. The factory does not silently bind them.

## Consequences and verification

Applications can migrate callers individually, with legacy and independent
clients in the same process. Service arguments and response types stay generated
from the API specification, including the existing public/preview boundary.
The generated diff is mechanical; post-processors remain the source of truth.

Concurrent local HTTP tests verify separate origins, bearer tokens, cancellation,
timeouts, and legacy coexistence. Tests also cover in-flight configuration
rotation, retry and logger separation, idempotency, binary results, typed errors,
detached methods, TypeScript capability boundaries, and generator drift.
The installed-package smoke test exercises the public exports and declarations.
