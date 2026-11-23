# Quickstart

Starting in an empty directory? [Deploy a private Node app on Free](#deploy-a-private-node-app-on-free).

Using a coding agent with an existing FastAPI repo? Follow the
[FastAPI agent quickstart](agents.md#agent-quickstart-for-an-existing-fastapi-repo)
to check compatibility and verify the deployed release through MCP.

Gregale deploys stateless APIs into isolated Firecracker microVMs. The fastest
guided path from an existing project to a URL is:

```bash
curl -fsSL https://get.gregale.dev | sh
gregale start ./my-api
```

Run `gregale start` inside your project, or pass its directory as shown above.
An unambiguous app is selected automatically; multiple candidates offer a picker.
When the directory holds two or more services (a Compose file or a workspace),
Start first offers to deploy all of them as one project: it then shows the
project plan from `gregale deploy --project` and creates nothing until you
confirm it. See [Deploy a multi-service app](multi-service-apps.md).
Review the launch and confirm. It handles browser login when needed, picks the app name and access
defaults, checks the source, shows compact launch progress, and automatically tests
the first response. It finishes with the response, elapsed time, useful follow-up
commands, and the tested URL when public ingress was verified. If the inferred app name already exists, choose
whether to update it or create an app with another name. The review shows your
account's app allowance and compute usage when available. Build logs are available
through `gregale logs`.

After you finish, Start remembers the app in the selected source directory.
From there, `gregale logs --follow`, `gregale metrics`, `gregale inspect`, and
`gregale deploy` reuse that target. Use `gregale config context get` to see or change your
defaults. For an isolated edit-and-reload loop, run `gregale dev`; see
[reusable development setup](gregale-dev.md#save-setup-for-the-next-session-or-a-teammate).

Start checks server compatibility before review and again before remote changes.
An incompatible installation or verification-service outage stops submission with
guidance; an earlier saved launch remains recoverable. Update the installation or
retry when the service is available, then run `gregale start` again.
If an upload's acceptance reply is lost, run Start again to check its saved
pending submission before launching another revision. Supported upload sessions
recover the exact accepted deployment without uploading source again.

If the selected source is linked with `gregale link`, Start uses its project,
app, and environment. A linked project with several workloads and no default app
offers a workload picker. The review shows the selected target; configuration
checks, secret uploads, and deployment use that environment. An unlinked source
uses the `default` environment, even when selected from another linked directory.

No project handy? Run `gregale start` from an empty directory and choose
**Create a starter**. Node.js, Python, and Go HTTP apps are available. A starter
also offers an optional greeting change: type your greeting and confirm one
edit-and-deploy preview to see it live. Press Enter to finish instead.
`start` deploys your working files, including
uncommitted changes; use `gregale deploy --dry-run` to preview the inferred build.

For your own project, the first successful response offers **Finish** or
**I've made changes — review and deploy again**. Edit locally, review the changed
file names and refreshed configuration, then confirm the update to the same app
and environment. Start checks the new deployment and offers the same loop again.
An interrupted or failed update keeps the accepted ID and the previous successful
check's deployment ID for recovery.

Interrupted after deployment was accepted? Repeat `gregale start [path]` with the same
original project directory and choose **Continue**. Source blockers offer a fix or recheck only when
needed. A failed first request offers a retry using the accepted deployment and a
short log preview. For deployment options, private access changes, and automation,
use the regular `gregale deploy` and `gregale app` commands. See
[guided deployment sessions](cli/deploy.md#your-first-deployment-with-gregale-start).

Required runtime keys are checked against the selected app's reviewed-scope
environment and secret metadata and any secrets file you select. A local `.env`
alone does not configure the app: choose it as a secrets file when prompted,
then review the key names before confirming deployment. Values are sealed before
upload and the selected file is excluded from source. Existing key names do not
prove that values are valid or that the app has adopted them.

Switching accounts, API endpoints, or linked targets offers a separate launch and keeps the
previous recovery record. Return to the original context to continue that launch.

Public response checks in named environments use the accepted deployment's URL.
Private response checks use the authenticated control plane and stay pinned to the
accepted deployment and environment. They report the actual HTTP status without
forwarding account credentials to the app. Application-owned authentication can
still deny the GET; a private check does not establish public access. Older servers
need deployment response-check support before these checks can complete.
Every launch, including the first launch and resumed sessions, requires evidence
for the accepted deployment. A successful public response
that omits its deployment identity is followed by an owner-authorized check,
labeled separately from public URL verification. Gateway health responses cannot
verify the app; choose another GET path that reaches an application route.

## Know what was verified

| Result | What it establishes |
| --- | --- |
| Deployment ready | The deployment reached the platform's ready state. |
| Application response verified | An application route returned 2xx from the accepted deployment. The output names the deployment and check method. |
| Public access verified | A credential-free request reached that deployment through its public URL. An owner-authorized check alone does not establish this. |
| Rollout complete | The accepted deployment reached 100% of traffic in its scope. Use `gregale release wait DEPLOYMENT_UUID --rollout` to wait for this separately. |

`gregale start` checks the first response automatically. For a public app, it
tries the deployment URL without account credentials. If the public response
cannot identify its deployment, it performs an owner-authorized check and labels
that result separately. For a private or internal app, it checks the exact
deployment through your authenticated Gregale account.

## Test a private app

Use the deployment UUID from your launch receipt to repeat an owner-authorized
GET, on any plan:

```bash
gregale release verify DEPLOYMENT_UUID --app my-api --path /
```

This uses deployment-write access and the same verification service as Start.
It keeps the app's access settings and sends no account credentials to your
application. The result includes the actual application HTTP status and a bounded
response preview. Only 2xx verifies the response; application-owned authentication
can still return 401 or 403. The check can wake the app and use compute allowance.

In the dashboard, choose **Verify response** after **Deployment ready**. It runs
the same owner-authorized check, including on Free. **Open public request URL**
and curl are separate ways to test public access when public access is configured.

On plans where a new app is private, opening its URL without the required
credentials can return `401 unauthorized` even after a successful deployment.
You can test it without making it public. To intentionally allow anonymous
requests, review the access change and run:

```bash
gregale app update my-api --no-require-authn
```

Then test an application route on the public URL. The public `/healthz` can
answer from Gregale's edge without running your code; it is not application
response verification. An internal app has no public endpoint.

For a named environment, use the accepted deployment UUID. `release verify`
looks up the project and environment identities from the deployment's app; pass
`--project-id UUID --environment-id UUID` only to pin them explicitly. Checks
never fall back to another deployment or scope. A redirect (3xx) is not
verified: rerun with `--path` set to the route it leads to.

## Deploy a private Node app on Free

Start with an empty directory, a signed-in Gregale CLI, and an unused app slot.
Free supports internal HTTP services, which have no public ingress. For this
path, create an internal Compose service. The standalone `--require-authn`
option requires a plan that supports edge authentication.

With a CLI that lists `hello-node-private` in `gregale init --list`, scaffold
the same internal project and start the guided deploy.

```bash
gregale init --template hello-node-private --path ./my-private-node
gregale start ./my-private-node
```

Choose a directory with an unused app name, or give `init` a name with
`--name`. The starter uses that name for both the project and its app.

From an empty directory, you can also run `gregale start` and pick **Private
Node.js HTTP app (Free plan)**. The launch review shows **Internal only**.
After the first verified response, edit the greeting in `app/server.js`
and choose **I've made changes — review and deploy again**. Start checks
the exact new deployment through your Gregale account.

### Create the source

Choose an unused name in `APP`. Keep the same Compose service name and project
name when you edit the app later.

```bash
APP=private-node

cat > server.js <<'EOF'
const { createServer } = require('node:http');

createServer((request, response) => {
  response.setHeader('Content-Type', 'text/plain; charset=utf-8');

  if (request.method === 'GET' && request.url === '/') {
    response.end('Hello from private Node\n');
    return;
  }

  if (request.method === 'GET' && request.url === '/healthz') {
    response.end('ok');
    return;
  }

  response.writeHead(404).end('Not found');
}).listen(Number(process.env.PORT || 3000), '0.0.0.0');
EOF

cat > Dockerfile <<'EOF'
FROM node:22-alpine3.23 AS runtime

FROM alpine:3.23
RUN apk add --no-cache libatomic libstdc++
COPY --from=runtime /usr/local/bin/node /usr/local/bin/node
WORKDIR /app
COPY server.js ./
ENV PORT=3000
EXPOSE 3000
CMD ["node", "server.js"]
EOF

cat > compose.yaml <<EOF
services:
  ${APP}:
    build: .
    ports:
      - "127.0.0.1:3000:3000"
EOF
```

The loopback port mapping makes this an **internal HTTP** service. A lone
service with only `expose:` can be classified as a worker, which Free does
not support. The Dockerfile copies Node into a matching Alpine image with its
runtime libraries so this dependency-free example fits Free's filesystem
limit. The full Node image can exceed the limit even for a small app.
See [Compose visibility and ports](multi-service-apps.md#public-and-internal-services)
and [Free plan limits](plans.md).

### Deploy and verify

Preview the deployment. Check that `can_apply` is true and the workload has
`"class": "http"` and `"visibility": "internal"` before submitting it.

```bash
gregale --non-interactive --json deploy --project --path . --source=worktree --name "$APP" --dry-run
gregale --non-interactive --json deploy --project --path . --source=worktree --name "$APP" --yes --no-wait
```

`--no-wait` returns after submission. Copy the `deployment_id` from the entry
in the JSON receipt's `builds` array whose `slug` is your app. Set
`DEPLOYMENT_ID` to that UUID, wait for readiness, then verify `GET /`.

```bash
gregale --non-interactive --json release wait "$DEPLOYMENT_ID" --app "$APP" --timeout 10m
gregale --non-interactive --json release verify "$DEPLOYMENT_ID" --app "$APP" --path /
```

Expect the same `deployment_id`, `http_status: 200`, the greeting in `body`,
and `verified: true`. An owner-authorized check has
`public_access_verified: false`. The internal service stays private.

### Edit and verify the next release

Change the greeting in `server.js` and repeat the project deploy with the
same name and `--no-wait`. Set `DEPLOYMENT_ID` to the **new** receipt UUID.
Repeat the readiness wait and the exact-release verification. Check that the
body contains the edited greeting.

The new release can answer its exact response check as soon as it is live
while the default canary is still shifting app traffic. To wait for the
canary to finish, run this command with the new UUID.

```bash
gregale --non-interactive --json release wait "$DEPLOYMENT_ID" --app "$APP" --rollout --timeout 10m
```

The canary advances on its own. If a wait is interrupted, repeat it with the
same UUID or follow progress with `gregale release traffic status "$APP"`.
The project receipt's `resume_command` also checks the existing submission
through rollout and configuration. See [progressive rollouts](rollouts.md).

## Keep building

```bash
gregale logs my-api --follow
gregale metric my-api
gregale inspect my-api
```

If a deploy fails, a request errors or the first request is slow, see
[Troubleshooting](troubleshooting.md).

For scripts and coding agents with a standalone app, use the non-interactive
deployment commands. To start with a private Node project on Free, follow the
[internal Compose quickstart](#deploy-a-private-node-app-on-free).

```bash
gregale --non-interactive --json deploy --path ./my-api --source=worktree --name my-api --yes
gregale --non-interactive --json release verify DEPLOYMENT_UUID --app my-api --path /
```

Use the accepted UUID in the deployment receipt. A response check exits nonzero
for a non-2xx application status; JSON includes `verified` and
`public_access_verified` separately. See [coding agents](agents.md) for credentials
and the automation contract.

Apps park when idle and wake on the next request; this saves idle cost but the
first request after a park can be slower. Gregale is stateless: use [sealed
secrets](secrets.md), [environment variables](env.md), object storage, or a
managed database for durable state.

For CI, use the pinned [GitHub deploy action](deploys.md#github-actions) or
`gregale deploy --github` to print its workflow snippet.
