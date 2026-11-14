# ADR-20261114141449: Offline OpenAPI tool generation

Status: Accepted
Amends: ADR-20261112200001, ADR-20261113180001

## Decision

Generate Node MCP adapter source from an explicitly supplied OpenAPI 3.0/3.1
JSON or YAML document, selected operation IDs and an explicit public HTTPS
server. Reuse the existing Node starter, source review, behavioral fixtures,
isolated builders, catalog verification, publication journal and exact grants.
Generation performs no provider request, dependency installation or tool call.
Document servers, examples and annotations neither select endpoints nor grant
authority. No native API, migration, execution service or VM behavior changes.

The CLI entry point is `gregale mcp server import`. The opt-in `build-tools`
catalog adds read-only `gregale_generate_tools_from_openapi` under the existing
local/hosted publishing access boundary. The MCP tool uses a fixed CLI command
with document bytes on stdin and a private temporary destination, never an
agent-supplied host path. It returns deterministic project/document digests and
base64 file chunks. CLI/bridge generator disagreement fails closed.

The first slice supports public GET/POST/PUT/PATCH/DELETE operations, scalar
path/query parameters, JSON request bodies and exactly one concrete JSON success
response. Parameters use separate path/query/body objects. Local acyclic schema
references, typed objects/arrays/scalars, enum and common size/numeric bounds are
supported. Unsupported authentication, parameter encodings, schema keywords,
remote references, duplicate keys and YAML aliases are refused. Examples and
defaults do not fill arguments. Schema formats remain annotations.

Generated adapters pin the selected server and operation paths. They validate
arguments and response schemas, wrap successful response values in an MCP
`result` property, and return stable errors without upstream error bodies.
Requests send no ambient credentials or cookies, follow no redirects and never
retry. DNS results must all be public and are used directly for the socket
connection while normal HTTPS hostname/certificate verification remains active.
Private, reserved, mapped and transition addresses are refused. Normal tenant
egress restrictions also apply after deployment.

Bounds live in `pkg/api/limits.go`: 256 KiB documents, 16 selected operations,
16 levels and 4,096 nodes, a 64-KiB normalized manifest, 32-KiB input and response
bodies, 8-KiB request URLs and a 15-second complete request deadline. Existing
source/upload, publication, invocation and plan quotas remain authoritative.

Generated fixture examples contain null expectations and have no accepted
fixture filename. Owners/agents must supply real behavioral assertions before
the normal reviewed publication workflow can start. Generated contract tests
exercise wiring and refusal, not provider behavior. Publication does not grant
invocation or claim that an HTTP method cannot have side effects.

## Verification and scope

Portable checks cover parser ambiguity and bounds, exact operation selection,
schema references and refusal, DNS/URL/response/cancellation fences, atomic
project creation, both MCP protocol versions, hosted source transfer and the
existing single-dispatch publication handoff. Generated Node adapters undergo
real local MCP discovery/calls with an injected upstream implementation.

This remains a preview authoring feature. Deployed staging and real-provider
acceptance remain separate evidence; the previously skipped live run is not
reintroduced. Private upstream credentials, arbitrary OpenAPI dialects,
schema composition and hosted Go/Python publication are separate milestones.
