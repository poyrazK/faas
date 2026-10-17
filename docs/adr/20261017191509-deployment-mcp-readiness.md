# ADR 20261017191509: Public deployment MCP readiness

The existing MCP deploy path already checks candidate replicas, catalog pins,
role authorization and traffic-admission evidence. We add a reusable, read-only
public-endpoint readiness report to that path rather than another promotion API.

`gregale mcp readiness --app SLUG --deployment UUID` first resolves the app and
verifies that the live deployment belongs to it, then obtains its public preview
URL. It makes no platform mutations and never forwards the platform API key to
the MCP server. `--token-env` supplies a separate, least-privilege MCP probe token.
The command is suitable for zero-traffic candidates and already-serving revisions.

The probe initializes a fresh stateless client, checks protocol negotiation,
sends the initialized notification, performs ping and validates tools/list.
Every successful response must carry exactly the requested protocol-version
header and must not establish a session. Redirects are refused. Existing response
size and pagination bounds apply, and the probe has a maximum 30-second deadline.
Neither tools nor resource contents nor rendered prompts are executed/read.
Resource/prompt-only servers are checked through their advertised metadata APIs
without requiring an unsupported tools/list method.

An optional reviewed `--baseline` pins the exact role-visible contract, including
additions. Format-1 locks cover tools; format-2 locks also query advertised catalog
metadata and cover capabilities, extensions, resources, templates and prompts.
The SHA-256 in the result hashes that canonical probe contract, not handler code
or a host health receipt. A report contains only timestamps, protocol, counts,
digest and fixed failure categories; it excludes credentials, definitions,
server errors, response bodies and private resource identifiers.

`mcp deploy` now includes fresh readiness results per release role and deployment
in its JSON receipt. Failure prevents the guarded traffic-promotion call, leaving
the candidate at zero traffic and the previous revision serving. Existing exact
replica, catalog, access and conditional-serving guards remain necessary: one
public endpoint check does not prove every replica is healthy. No arbitrary tool
execution is inferred from readOnlyHint. Explicit smoke fixtures and their
`--allow-tool-execution` opt-in retain their separate behavior.

Standalone reports are point-in-time client observations, not persisted platform
receipts or reusable promotion tokens. They can be saved as CI artifacts using
`--json` and output redirection. This change does not gate unrelated deployment
APIs, add a background synthetic monitor or introduce a server-side readiness
attestation. The deploy command reruns probes immediately before its existing
promotion path rather than trusting a saved report.
