# ADR-20261121160000: MCP hosting fixes from the 2026-11-21 prod probe

- Status: Accepted
- Date: 2026-11-21
- Related: ADR-20261026195952 (hosted MCP), ADR-20261109130000 and
  ADR-20261110160000 (guest connection resets), ADR-1150 (request timeouts),
  ADR-20261121140000 (MCP reliability)

## Context

A prod probe of customer MCP hosting (`gregale mcp server init/deploy`, CLI
rc.362 against rc.363) deployed the Node, Go and Python starters, a private
(external-OAuth) Go starter with a self-hosted JWKS, and a probe server with
long-running, large-payload and outbound-fetch tools. It found:

1. Readiness sent `ping`, which protocol 2026-07-28 removed (the official
   `@modelcontextprotocol/server` 2.4.0 answers -32601). The default Node
   starter and any official-SDK server could never promote.
2. The Go and Python starters refused `initialize` with HTTP 400 for any
   revision but 2026-07-28 and 2025-11-25. Clients on 2025-06-18 (TypeScript
   SDK 1.17) could not connect.
3. Every tool call slower than 30 s returned 503 `request_budget_exceeded`
   (the HTTP default), and the Go starter cut every response after a fixed
   15 s write deadline.
4. One failed JWKS fetch made all three starters reject every token for 30 s;
   deploy verification hit that window every time.
5. After a cold wake, an app's first call on a pooled keep-alive connection
   hung until its client timeout (5 of 6 probes): runtime-admission promotion
   resumed a snapshot-restored guest without the inherited-connection reset.
6. `mcp server deploy` onto an app protected by default refused only after a
   full build, without naming the fix.

## Decision

- **Readiness** skips `ping` on 2026-07-28; `server/discover` is that
  revision's liveness exchange. Pre-2026 revisions still require `ping`.
- **Starter negotiation (Go, Python).** Legacy mode also serves 2025-06-18
  and 2025-03-26 (the same wire format for tools, resources and prompts).
  `initialize` negotiates from its body: an unserved revision gets the newest
  revision the server speaks, never an error. A request with neither a version
  header nor a `_meta` declaration is 2025-03-26, as the spec requires. Node
  already negotiates through the SDK.
- **Request timeout.** A first MCP release raises an unset app request timeout
  to min(300 s, plan ceiling): Free 30, Hobby 60, Pro 300, Scale 300 (Scale
  may raise it to 900 with `gregale app <slug> --request-timeout`). An owner's
  explicit timeout is kept.
- **Go starter** renews its 15 s write deadline on every write, so it still
  bounds a stalled reader without capping tool run time.
- **JWKS.** A failed fetch backs off 1 s in all three starters; the 30 s
  throttle stays only for a successful key set lacking the token's kid. The Go
  starter's JWKS client does not pool connections.
- **Promotion** resets inherited guest connections like the wake and
  warm-spare paths. A failed reset after restore logs a warning at most once a
  minute per vmmd, since it is customer-visible.
- **Ingress precheck.** `mcp server deploy` refuses a serving app behind
  require_authn, public auth or disabled streaming before uploading, naming the
  exact `gregale app` commands. Opening a protected app stays explicit.

## Consequences

- Official-SDK MCP servers and the Node starter can promote once this ships
  in the CLI (the starters and the deploy check are CLI-embedded) and apid
  (readiness monitor, private publication).
- Not fixed here, still open:
  - Request bodies of 4 MB and more reach the client as an HTTP/2 reset
    instead of the app's 413. Which hop resets needs a node to reproduce.
  - About 30% of redeploys onto a serving app failed with "public candidate
    verification remained unavailable". The candidate VM logged nothing;
    the cause needs schedd and imaged logs.
