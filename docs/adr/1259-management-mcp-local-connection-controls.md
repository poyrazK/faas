# ADR-1259: Owner-protected management MCP connection controls

## Status

Accepted. Extends ADR-1258.

## Decision

Expose an optional operator HTTP handler on a Unix-domain control socket,
independent of the public OAuth/MCP mux. `NewHTTPService` exposes separate public
and admin handlers; `NewHTTPHandler` retains its existing contract. No new MCP
agent tools or public operator routes are registered.

`gregale mcp serve --admin-socket PATH` requires HTTP transport, a clean absolute
path of at most 100 bytes, and an existing current-user-owned directory with mode
0700 and no symlink components. Refuse existing paths, including stale sockets.
Create the socket with mode 0600 before serving. Close/unlink it during shutdown.
Unix ownership/mode checks protect both listener and CLI client; no TCP fallback,
proxy, redirect or API-key authentication is used. Privileged root remains within
the operating system trust boundary. Unsupported platforms reject socket use.

Provide `mcp connections list --socket PATH` and `revoke --socket PATH --grant-id ID`.
List sorted active grant IDs with bounded pagination and explicit credential-free
summaries: client/account IDs, scopes, expiry, app access and selected counts,
named/writable environment selectors and default-write consent. Selector arrays
are limited to twenty entries with counts/truncation flags. Enforce the existing
128 KiB response bound and never serialize native keys, bearer/refresh tokens,
hashes, browser codes or pending authorization details.

Revoke one exact grant under the bridge lock, stop its subscriptions, and cancel
its active requests using a private per-grant lifecycle. Gate requests authenticated
before revocation as well as new bearer requests. Other grants retain independent
lifecycles. Persist the OAuth snapshot before acknowledging durable revocation.
A failed commit reports failure and leaves bridge storage fail-closed; a missing
grant returns 404. Ephemeral bridges report `durable: false` explicitly.

Track private control handlers in process draining, alongside the main HTTP server,
so admin journal writes complete before state close. Operator controls are disabled
by default and inherit existing mutation/authorization audit records.

## Consequences

Operators can inspect and disconnect agent grants without possessing their tokens.
Revocation survives a restart using the same acknowledged encrypted journal, but
does not revoke an account's native API keys or undo already accepted operations.
Inspect native deployment state before planning a replacement after an ambiguous
write on a revoked grant. Connection metadata is operator-private and is separate
from identifier-free Prometheus metrics. An existing socket is never automatically
replaced; after an abnormal exit, the owner must verify it is stale before removing it.
