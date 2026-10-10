# mcp-go

A small Go MCP server using the official Go SDK. It serves stateless Streamable
HTTP at `/mcp`, exposes three tools, a static resource, a resource template and a
prompt, and reports readiness at `/healthz`. It starts in open mode and rejects
browser `Origin` headers unless you add the exact origin to `allowed_origins`.

The generated `go.mod` declares `go 1.26.9`,
`github.com/modelcontextprotocol/go-sdk v1.8.0`, and
`github.com/jackc/pgx/v5 v5.11.0` for PostgreSQL-backed Tasks.

The starter supports application-side external OAuth resource-server checks,
including RS256/ES256 JWT verification against a remote JWKS, protected-resource
metadata, bearer challenges, and per-entry scope checks for tools, resources,
resource templates and prompts. The OAuth provider owns login and token
issuance. This starter does not include legacy transport. It advertises safe,
prefix-matched completions for `summarize.style` and the
`customer://records/{recordId}` resource template; completion access follows
the same prompt and resource scope policies as prompt reads and resource reads,
and customer-record suggestions are filtered by verified caller ownership.

For OAuth, set `auth.mode` to `external-oauth`, provide HTTPS `issuer`,
`jwks_url` and canonical `resource` URLs, and set nonempty endpoint `scopes` in
`gregale-mcp.json`. `tool_scopes`, `resource_scopes` and `prompt_scopes` are
optional allowlists; an empty scope array allows any caller with the endpoint
scopes, and an omitted map keeps endpoint-only access for that catalog type.
The default open config lists the harmless sample entries with empty arrays.
Handlers can call `verifiedCallerIdentity(principal)` to get the verified token
subject and client ID without carrying the token into application code. The
sample customer records are owned by `demo-caller-a` (`example-1`, `example-3`)
or `demo-caller-b` (`example-2`); open mode keeps these synthetic examples
public. Replace the sample owner map with a database lookup scoped by the
verified subject before serving real customer data.

## Run locally

`gregale mcp init --language go` materializes `go.mod`. Then run:

```sh
go test ./...
go run .
gregale mcp doctor --url http://127.0.0.1:8080/mcp
gregale mcp tools --url http://127.0.0.1:8080/mcp
gregale mcp call --url http://127.0.0.1:8080/mcp --tool greet --arguments '{"name":"Ada"}'
gregale mcp resources --url http://127.0.0.1:8080/mcp
gregale mcp resource-read --url http://127.0.0.1:8080/mcp --uri 'customer://records/example-1'
gregale mcp prompts --url http://127.0.0.1:8080/mcp
gregale mcp prompt-get --url http://127.0.0.1:8080/mcp --prompt summarize --arguments '{"text":"weekly report"}'
gregale mcp complete --url http://127.0.0.1:8080/mcp --prompt summarize --argument style --value exec
gregale mcp complete --url http://127.0.0.1:8080/mcp --resource-template 'customer://records/{recordId}' --argument recordId --value example-
```

For a protected deployment, pass an MCP access token with `--token-env
MCP_TOKEN`. Keep that token out of shell history. Add object or tenant ownership
checks inside resource and tool handlers before connecting real customer data.

## Durable MCP Tasks

`build_report` supports the Gregale Tasks extension when `tasks.enabled` is true.
Set `DATABASE_URL` to a PostgreSQL connection string, provide a stable
`MCP_TASK_OWNER_KEY` secret with at least 32 bytes, and set `FAAS_APP_ID` to a
stable namespace. The default config names these environment variables already;
enable Tasks by setting `tasks.enabled` to `true` in `gregale-mcp.json`.

When the table is absent, startup creates the base `gregale_mcp_tasks` schema; for
an existing migrated schema it checks the required columns without trying to
change database objects. A fresh database therefore needs schema-create rights,
while a migrated runtime role needs table DML rights. Arguments, results and errors
are encrypted with AES-256-GCM; task ownership is scoped to the
verified OAuth resource, subject and client ID. Tasks from open-mode requests
share an `open` owner. The default queue limits are 1,000 outstanding tasks per
namespace and 100 per owner. One in-process worker claims tasks with a lease, so a
restart recovers queued tasks and reclaims expired running leases. Keep the owner
key stable while task rows remain in PostgreSQL.

For a shared task database with the Node starter, use the same namespace, owner
key and OAuth resource. Go and Python currently read and write the legacy v1
payload envelope; keep Node's active payload key set to `legacy` while sharing
tasks. Non-legacy Node payload-key rotation is not supported by these starters.

Use a Task-capable client to call the sample tool, then read or cancel its task:

```sh
gregale mcp call --url http://127.0.0.1:8080/mcp --tasks \
  --tool build_report --arguments '{"report":"weekly","steps":8}'
gregale mcp task-wait --url http://127.0.0.1:8080/mcp --task-id TASK_ID
gregale mcp task-cancel --url http://127.0.0.1:8080/mcp --task-id TASK_ID
```

The starter's `tasks/update` endpoint follows the protocol response contract; its
sample handler does not request client input. The Go SDK does not expose a public
Task result setter, so Task wire requests are handled at the stateless HTTP edge.
If the PostgreSQL database is also used by the Node worker, run the Node
`tasks:migrate` workflow to prepare its worker, admission and key-registry tables.

## Deploy

```sh
gregale mcp deploy --path . --name <slug>
```

The deploy command stages a zero-traffic candidate, verifies its MCP endpoint,
and promotes only after the previous serving revision still owns all traffic.
