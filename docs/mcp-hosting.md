# Host an MCP server

Build an MCP server, test it on your machine, and deploy it to Gregale so
Claude, Cursor, VS Code and other MCP clients can call its tools. Gregale runs
it as an ordinary app that speaks Streamable HTTP at `/mcp`, scales it to zero
when nobody calls it, and checks every new version before it takes traffic.

MCP hosting is in preview on the Hobby, Pro and Scale plans. Run
`gregale feature list` and look for `mcp-hosting` to see whether it is on for
your account.

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

The files that matter:

| File | What it is |
|---|---|
| `app.js` (Node), `main.go`, `server.py` | Your tools, resources and prompts. |
| `gregale-mcp.json` | The MCP contract: the `/mcp` endpoint, who may call it (`auth`) and which browser origins are allowed. |
| `gregale.yaml` | How Gregale runs the app: start command, port `8080` and health path `/healthz`. |
| `README.md` | How to add tools and change the catalog while the server runs. |

Add a tool in `app.js` (the README covers resources, prompts and changing the
catalog while the server runs):

```js
mcp.registerTool('lookup', {
  description: 'Find a record by key.',
  inputSchema: z.object({ key: z.string().min(1) }),
}, async ({ key }) => ({
  content: [{ type: 'text', text: await lookupRecord(key) }],
}));
```

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
- It raises the request timeout to 300 seconds or your plan's maximum,
  whichever is lower (Hobby 60, Pro and Scale 300), so slow tools don't fail
  after the usual 30 seconds. A timeout you set yourself is kept. On Scale you
  can go up to 900 seconds with `gregale app my-mcp --request-timeout 900`.

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

You can require extra scopes for individual tools, resources and prompts in
`gregale-mcp.json` (`tool_scopes`, `resource_scopes`, `prompt_scopes`). A
caller sees only the entries its token allows. Scopes decide which tools a
caller may use; your tool code must still check that the caller owns the
records it asks for.

Deploy with a valid token so Gregale can verify the new version:

```sh
gregale mcp server deploy --path ./private-mcp --name private-mcp --token-env MCP_ACCESS_TOKEN
```

The token is read from the environment variable and never written to your
source.

**Browser clients.** `allowed_origins` in `gregale-mcp.json` is empty, which
rejects every request that carries a browser `Origin` header. Desktop and CLI
clients normally don't send one. Add the exact origins of browser-based clients you
trust.

## Idle servers and slow tools

When nobody calls the server, Gregale parks it and the next call wakes it. The
first call after a park takes longer; the ones after it run at normal speed.
See [How scaling to zero works](cold-wake.md).

For work that takes longer than your request timeout, return a durable MCP
Task instead of holding the request open. Create a server with
`--tasks --name <prefix>` to get separate web, worker and observer projects
backed by PostgreSQL; their README walks through the setup.

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
| The client gets `401` with `Bearer realm="apps"` | The app was deployed with `gregale deploy`, which protects it with a Gregale API key by default | Deploy with `gregale mcp server deploy`, which opens the app to MCP clients. |
| A tool call fails after 30 or 60 seconds with `request_budget_exceeded` | The tool ran past the app's request timeout | Raise it with `gregale app my-mcp --request-timeout <seconds>` up to your plan's maximum, or return a Task. |
| A browser-based client is refused | Its origin is not in `allowed_origins` | Add the exact origin to `gregale-mcp.json` and deploy again. |
| `403` naming the required scopes | The caller's token lacks a scope that `gregale-mcp.json` requires for that tool | Issue the token with the scope, or change the tool's scopes. |
| The deploy fails and the old version keeps serving | Gregale could not list the new version's catalog | Run `gregale mcp server doctor` against your local server and fix what it reports. With external OAuth, check that `--token-env` names a valid token. |

For error codes, see [Errors](errors.md).
