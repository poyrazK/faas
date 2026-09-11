# Quickstart

Gregale deploys stateless APIs into isolated Firecracker microVMs. The fastest
path from an existing project to a URL is:

```bash
curl -fsSL https://get.gregale.dev | sh
gregale login
cd my-api
gregale deploy
```

The CLI detects a supported framework, creates the app when needed, streams the
build, waits for readiness, and prints the verified URL. Use
`gregale deploy --dry-run` to inspect the inferred build before uploading.

No project handy? Start with a maintained template:

```bash
gregale deploy --template hello-node
```

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
