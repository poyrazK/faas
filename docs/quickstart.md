# Quickstart

Gregale deploys stateless APIs into isolated Firecracker microVMs. The fastest
guided path from an existing project to a URL is:

```bash
curl -fsSL https://get.gregale.dev | sh
cd my-api
gregale start
```

`start` takes no flags. Choose your project or a starter, review the launch,
and confirm. It handles browser login when needed, picks the app name and access
defaults, checks the source, shows compact launch progress, and automatically tests
the first response. It finishes with the exact tested URL, response, elapsed time,
and useful follow-up commands. Build logs are available through `gregale logs`.

No project handy? Choose **Create a starter** inside the session. Node.js, Python,
and Go HTTP apps are available. A starter also offers an optional greeting change:
type your greeting and confirm one edit-and-deploy preview to see it live. Press
Enter to finish instead. `start` deploys your working files, including
uncommitted changes; use `gregale deploy --dry-run` to preview the inferred build.

Interrupted after deployment was accepted? Run `gregale start` again from the same
directory and choose **Continue**. Source blockers offer a fix or recheck only when
needed. A failed first request offers a retry using the accepted deployment and a
short log preview. For deployment options, private access changes, and automation,
use the regular `gregale deploy` and `gregale app` commands. See
[guided deployment sessions](cli/deploy.md#your-first-deployment-with-gregale-start).

Useful follow-ups:

```bash
gregale logs my-api --follow
gregale metrics my-api
gregale open my-api
gregale capabilities
```

Apps park when idle and wake on the next request; this saves idle cost but the
first request after a park can be slower. Gregale is stateless: use [sealed
secrets](secrets.md), [environment variables](env.md), object storage, or a
managed database for durable state.

For CI, use the pinned [GitHub deploy action](deploys.md#github-actions) or
`gregale deploy --github` to print its workflow snippet.
