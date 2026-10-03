# MCP preview qualification, 2026-10-01

The first-class `gregale mcp deploy` flow deployed the official-SDK Node starter
to the existing native Gregale host. Discovery, Origin rejection, legacy
stateless compatibility, a harmless `add` call, and live progress passed. After
`gregale park`, discovery reported `wake_tier: restored` and streaming passed
again. The app was parked after qualification; `ps --json` returned no active
instance rows (empty NDJSON, represented as an empty array in `instances.json`).

App: `gregale-mcp-demo`, endpoint: `https://gregale-mcp-demo.gregale.dev/mcp`.
Deployment: `48ed1b5b-ba2b-42ba-a81e-82004b709ba4`.
Source SHA-256: `28e79bb42459c62ab5565da0d736bfdf141de052e66c7506e5d90000866af43e`.

The official TypeScript SDK client 2.2.0 independently passed discovery, `add`,
`greet`, live progress and a legacy call against this deployment. Its first
request also reported `restored`; `sdk-client.json` records the results. The
app was parked again after that client check.

The JSON receipts contain only the public demo contract and harmless results.
They exclude account credentials, email, user IP, and tool arguments.
The observed gateway classification was `accept-json-downgrade`; the response
was still SSE, with progress arriving before completion. Client-observed elapsed
time includes public networking and does not establish platform restore p95.

## Release caveat

The deployed apid still rejected 78 valid npm SRI checksum fields as source
secrets. This deployment used a temporary demo copy that removed those fields
while preserving the same pinned versions and resolved package URLs. The
checked-in starter keeps every checksum. The production source scanner must be
upgraded and deployment repeated with the untouched starter before claiming a
fully reproducible release qualification. Secret scanning was never disabled.

The scanner regression and the actual apid source-ingress fixture pass locally:
the complete starter is accepted and an added provider credential is rejected.
Portable tests cover the CLI journey, maintenance after failed verification,
bounded diagnostics, OAuth JWT validation, Origin/CORS, redacted logs, and
disconnect cancellation. The GitHub Actions MCP contract lane repeats the SDK
and CLI integration. External provider/client login interoperability and durable
Tasks remain outside this preview's qualification.

## Commands

```sh
gregale mcp deploy --path <starter> --name gregale-mcp-demo --profile small --json
gregale mcp call --app gregale-mcp-demo --tool add --arguments '{"a":7,"b":5}' --json
gregale mcp doctor --app gregale-mcp-demo --legacy --stream-tool stream_demo --json
gregale park gregale-mcp-demo --json
gregale mcp doctor --url https://gregale-mcp-demo.gregale.dev/mcp --legacy --stream-tool stream_demo --timeout 60s --json
gregale park gregale-mcp-demo --json
gregale ps gregale-mcp-demo --json
```
