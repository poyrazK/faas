# Quickstart

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
`gregale deploy` reuse that target. Use `gregale context` to see or change your
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

Useful follow-ups:

```bash
gregale logs my-api --follow
gregale metric my-api
gregale open my-api
gregale feature list
```

Apps park when idle and wake on the next request; this saves idle cost but the
first request after a park can be slower. Gregale is stateless: use [sealed
secrets](secrets.md), [environment variables](env.md), object storage, or a
managed database for durable state.

For CI, use the pinned [GitHub deploy action](deploys.md#github-actions) or
`gregale deploy --github` to print its workflow snippet.
