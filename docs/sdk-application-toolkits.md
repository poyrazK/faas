# Deploy an application toolkit (SDK preview)

Compose typed query tools, resource reads, pipelines, compensation, and outbound
actions into one application MCP catalog. A toolkit selects the operations to
publish and declares the scopes a caller needs for each operation. The Node and
Python MCP starters install that catalog on their existing authenticated endpoint.

## Select operations and scopes

Build the existing adapters first. Select each operation by its advertised name.
This example publishes a search tool, an owned report resource, and two operations
from a recovery adapter.

```ts
import { createApplicationToolkit } from '@gregale/sdk-node/preview';

const toolkit = createApplicationToolkit([
  {
    adapter: queries,
    toolScopes: { csv_import_rows: ['imports:read'] },
    resourceScopes: { [report.template.uriTemplate]: ['reports:read'] },
  },
  {
    adapter: recovery,
    toolScopes: {
      recover_csv_cleanup_start: ['imports:recover'],
      recover_csv_cleanup_status: ['imports:read'],
    },
  },
]);
```

```python
from faas_sdk.preview import ApplicationToolkit, ToolkitMount

toolkit = ApplicationToolkit([
    ToolkitMount(
        queries,
        tool_scopes={"csv_import_rows": ["imports:read"]},
        resource_scopes={report.template["uriTemplate"]: ["reports:read"]},
    ),
    ToolkitMount(
        recovery,
        tool_scopes={
            "recover_csv_cleanup_start": ["imports:recover"],
            "recover_csv_cleanup_status": ["imports:read"],
        },
    ),
])
```

Here `queries` is a query MCP adapter registered with the report resource.
`recovery` is a compensation MCP adapter. The same mounting API accepts
pipeline and outbound action MCP adapters. Each published operation requires all
of its listed scopes. Omitting an operation leaves it outside the catalog and
denies direct calls. Duplicate names, resource templates, and unknown selections
fail during startup. The toolkit accepts up to 256 operations across 100 mounts.

Resource mounts accept the fixed `{id}` templates from typed query resources.
Discovery, reads, and resource links use the same exact template and scope rule.
A tool result can carry links to published resources the caller has scope to
read. Each resource read still checks the principal's current application access.

The toolkit snapshots schema descriptors and rules at startup. Its
`toolScopes` and `resourceScopes` are host configuration. For an existing custom
host, use `listTools(access)`, `listResourceTemplates(access)`, `call`, and
`readResource` after authenticating each request. Python names use snake case.
Pass a `ToolkitAccess` with the verified principal and granted scopes. The
unfiltered descriptors are for host registration.

## Register with a starter

Scaffold the Node or Python MCP starter and add a dependency on the SDK version
that includes the toolkit preview. Configure the starter for external OAuth.
Use an issuer and audience for the application's MCP endpoint and grant the
endpoint scope along with each caller's operation scopes.

In the Node starter's `server.js`, pass the toolkit when constructing the app.

```js
const { app, handler, beginDrain, closeTaskSubscriptions } =
  createApp(config, { taskRuntime, toolkit });
```

For Python, build the toolkit in your application entry point and
call the starter's existing `main`.

```python
from server import main

main(toolkit=toolkit)
```

The starter scopes both discovery and direct calls. The toolkit passes the verified
OAuth subject to your existing adapters. Use `resolveToolkitPrincipal` in Node
or `resolve_toolkit_principal` in Python to map the verified caller to your
application's principal type. The resolver receives the subject, client ID, and
scopes. A null result denies access. Keep Node resolvers bounded and cooperative
with the supplied cancellation signal. Python resolvers run synchronously and
adapter handlers run in the request's async loop. Open loop-bound clients in the
handler, as shown by the CSV backend.

Tool and resource policies from the toolkit join the starter's
`auth.tool_scopes` and `auth.resource_scopes` at startup. The same name or
template cannot appear in both. The resulting maps are allowlists. To retain
a sample or previously registered operation, explicitly include its policy in
the starter configuration. The merged catalog and scope policy participate in
the existing health fingerprint.

The adapter remains responsible for business authorization, input and result
validation, and each workflow's approval and dispatch rules. Issue short-lived
OAuth tokens to bound the life of transport grants. Every request verifies its
token and calls the applicable adapter with the current principal. Retain
workflow request IDs across reconnects to read or reconcile the same receipt.

## Run the CSV example

The example registers an import search, failure report, and payment query with
the authorized cleanup and Stripe refund lifecycles. Support can inspect owned
imports. Operators can also request cleanup and refund. A separate authenticated
human reviews each effect. Both starters use the same operation scope fixture
and the same PostgreSQL receipts.

Follow the CSV example for the opt-in HTTP journey. It tests both endpoint
implementations with signed fixture tokens, real PostgreSQL and Chrome, and
simulated native workflow and Stripe responses.

Mount [typed inbox follow-ups](sdk-work-inbox-follow-ups.md) to return the
current operation and arguments for an inbox reference.

Enable [pipeline Task views](sdk-pipeline-tasks.md) when clients need a durable
MCP handle for selected pipeline starts.
