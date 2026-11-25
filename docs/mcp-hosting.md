# Host an MCP server

Build an MCP server, test it on your machine, and deploy it to Gregale so
Claude, Cursor, VS Code and other MCP clients can call its tools. Gregale runs
it as an ordinary app that speaks Streamable HTTP at `/mcp`, scales it to zero
when nobody calls it, and checks every new version before it takes traffic.

MCP hosting is available on the Hobby, Pro and Scale plans.

To let an agent manage your Gregale account instead, see
[Use Gregale from coding agents](agents.md).

## Create a server

```sh
gregale mcp server init --path ./my-mcp
```

This creates a Node server. Add `--runtime go` or `--runtime python` for the
other starters. Each one comes with sample tools (`greet`, `add` and
`stream_demo`), a resource, a resource template and a prompt, so it works
before you change anything.

The Node starter is also on GitHub as
[gregale-dev/mcp-server-node](https://github.com/gregale-dev/mcp-server-node),
and runs as-is at `https://mcp-server-node.gregale.app/mcp`. Connect a client
to that endpoint to try the sample tools before you create your own.

The files that matter:

| File | What it is |
|---|---|
| `server.js` and `app.js` (Node), `main.go` (Go), `server.py` (Python) | The server and its tools, resources and prompts. |
| `gregale-mcp.json` | The MCP contract: the `/mcp` endpoint, who may call it (`auth`) and which browser origins are allowed. |
| `gregale.yaml` | How Gregale runs the app: port `8080`, health path `/healthz` and, for Node and Python, the start command. |
| `README.md` | How to add tools and change the catalog while the server runs. |

To add a tool to the Node starter, register it in `server.js` before the
server starts listening. Keep the object `createApp` returns, register the tool,
then take the values the rest of the file uses from it:

```js
import * as z from 'zod/v4';

const mcp = createApp(config);
mcp.registerTool('lookup', {
  description: 'Find a record by key.',
  inputSchema: z.object({ key: z.string().min(1) }),
}, async ({ key }) => ({
  content: [{ type: 'text', text: await lookupRecord(key) }],
}));
const { app, handler, beginDrain, closeTaskSubscriptions } = mcp;
```

Then list the tool in `auth.tool_scopes` in `gregale-mcp.json`, even on an open
server. The scope maps are allowlists: a tool that is not listed is hidden and
refused with `403 tool_access_denied`. An empty list lets every caller use it:

```json
"tool_scopes": {
  "add": [],
  "greet": [],
  "lookup": [],
  "stream_demo": []
}
```

New resources and prompts go in `resource_scopes` and `prompt_scopes` the same
way. The README covers resources, prompts and changing the catalog while the
server runs.

## Run it locally

```sh
cd my-mcp
npm ci
npm test
npm start
```

In another terminal, check the protocol and call a tool:

```sh
gregale mcp server doctor --url http://127.0.0.1:8080/mcp --legacy
gregale mcp client call --url http://127.0.0.1:8080/mcp --tool add --arguments '{"a":7,"b":5}'
```

`doctor` checks discovery, origin rejection, protocol compatibility and
streaming. It lists tools without running them unless you name one with
`--stream-tool`.

For Go, run `go run .`; for Python,
`python -m pip install -r requirements.txt && python server.py`.

## Deploy

```sh
gregale mcp server deploy --path . --name my-mcp --size small
```

The deploy uploads your source, builds it, and starts the new version with no
traffic. Gregale then connects to it as an MCP client, lists its catalog, and
promotes it only if discovery works. If any check fails, the version that was
already serving keeps serving.

The first deploy also prepares the app for MCP clients:

- It turns streaming on and leaves the app without a Gregale API key, so MCP
  clients can reach it. Who may call it is then decided by `auth` in
  `gregale-mcp.json`.
- It raises the request timeout from the usual 30 seconds to 300 seconds
  (60 on Hobby, where that is the maximum), so slow tools don't fail. A timeout
  you set yourself is kept. Scale allows up to 900 seconds:
  `gregale app my-mcp --request-timeout 900`.

If the app already exists behind an API key or with streaming off,
`mcp server deploy` stops before uploading and names the `gregale app`
commands that open it.

Your server is now at:

```
https://my-mcp.gregale.app/mcp
```

## Connect a client

**Claude Code:**

```sh
claude mcp add --transport http my-mcp https://my-mcp.gregale.app/mcp
```

**Other clients:** print the connection settings and paste them into the
client's MCP configuration:

```sh
gregale mcp server export --app my-mcp --name my-mcp
```

The output contains no credentials. Check what a client will see from the
command line:

```sh
gregale mcp client tools --app my-mcp
gregale mcp client prompts --app my-mcp
```

## Control who can call it

A new starter is open: anyone with the URL can list and call its tools. Before
you add tools that touch private data, put the server behind your own OAuth
provider:

```sh
gregale mcp server init --path ./private-mcp \
  --auth external-oauth \
  --issuer https://identity.example.com \
  --jwks-url https://identity.example.com/jwks \
  --resource https://private-mcp.gregale.app/mcp \
  --scopes mcp:tools
```

The server then accepts only JWT access tokens from that issuer, for that
resource, with the required scopes. Your provider handles sign-in, consent and
token issuance. `--resource` must be the server's final public URL.

Each tool, resource and prompt in `gregale-mcp.json` (`tool_scopes`,
`resource_scopes`, `prompt_scopes`) can require scopes of its own. A caller
sees only the entries its token allows. Scopes decide which tools a
caller may use; your tool code must still check that the caller owns the
records it asks for.

Deploy with a valid token so Gregale can verify the new version:

```sh
gregale mcp server deploy --path ./private-mcp --name private-mcp --token-env MCP_ACCESS_TOKEN
```

The token is read from the environment variable and never written to your
source.

**Charge for it.** Signed-in callers can become your customers on a free
plan the first time they call, and move to a paid plan later; see
[MCP callers become customers](api-monetization.md#mcp-callers-become-customers).

**Browser clients.** `allowed_origins` in `gregale-mcp.json` is empty, which
rejects every request that carries a browser `Origin` header. Desktop and CLI
clients normally don't send one. Add the exact origins of browser-based clients you
trust.

## Idle servers and slow tools

When nobody calls the server, Gregale parks it and the next call wakes it. The
first call after a park takes longer; the ones after it run at normal speed.
See [How scaling to zero works](cold-wake.md).

A tool call must finish within the app's request timeout: 300 seconds after
the first deploy (60 on Hobby), up to 900 on Scale. Stream progress from long
tools (see `stream_demo` in the starter) so clients know the call is alive.

## Readiness checks and alerts

Every `mcp server deploy` runs a readiness check on the new version before it
gets traffic: it initializes the server, pings it and lists its catalog,
without calling tools. Run the same check yourself against any deployment:

```sh
gregale mcp readiness --app my-mcp --release <deployment-id> --baseline gregale-mcp.lock.json
```

`--baseline` is optional; with it, the catalog must match the lock file
exactly. For a server behind OAuth, pass a client token with `--token-env`.

To be told when a live server stops answering, add a readiness policy with
`monitor` enabled to the app (`PUT /v1/apps/my-mcp/mcp-readiness-policy`).
Gregale then probes it every 60 seconds or more. Alert on it with the
**MCP readiness monitor unhealthy** preset, or:

```sh
gregale alert create --app my-mcp --name "MCP readiness" --metric mcp_readiness_unhealthy \
  --comparison gt --threshold 0 --window-spec 5m \
  --webhook-url https://example.com/hook --webhook-secret-stdin
```

Monitoring and alerts never move traffic. `gregale mcp readiness --help` lists
incidents, maintenance windows and notification commands.

## Check a deployed server

```sh
gregale mcp server status --app my-mcp       # every serving replica and its catalog
gregale mcp server doctor --app my-mcp       # the same checks as locally, against the live URL
gregale logs my-mcp --follow                 # what the server prints
```

To catch catalog changes you didn't review, save the current tool definitions
and compare against them later:

```sh
gregale mcp server lock --app my-mcp
gregale mcp server watch --app my-mcp --baseline gregale-mcp.lock.json
```

## Common problems

| What you see | Cause | Fix |
|---|---|---|
| The client gets `401` with `Bearer realm="apps"` | The app was first deployed with `gregale deploy`, which protects it with a Gregale API key by default | Open it with `gregale app my-mcp --no-require-authn` (the deploy error names any other command needed, such as `--public-auth open` or `--streaming-enabled`), then deploy with `gregale mcp server deploy`. |
| A new tool is missing from the catalog, or calls get `403 tool_access_denied` | It is not listed in `auth.tool_scopes` | Add it with an empty scope list (or the scopes it needs) and deploy again. |
| A tool call fails after 30 or 60 seconds with `request_budget_exceeded` | The tool ran past the app's request timeout | Raise it with `gregale app my-mcp --request-timeout <seconds>` up to your plan's maximum. |
| A browser-based client is refused | Its origin is not in `allowed_origins` | Add the exact origin to `gregale-mcp.json` and deploy again. |
| `403` naming the required scopes | The caller's token lacks a scope that `gregale-mcp.json` requires for that tool | Issue the token with the scope, or change the tool's scopes. |
| The deploy fails and the old version keeps serving | Gregale could not list the new version's catalog | Run `gregale mcp server doctor` against your local server and fix what it reports. With external OAuth, check that `--token-env` names a valid token. |

For error codes, see [Errors](errors.md).
