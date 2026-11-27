# Deploy a web framework

Gregale builds a web app from its source with no Dockerfile: it reads the
project's manifest, picks a framework, and infers how to start it. This page
is what every framework has in common. The guides cover each one:

- [Express, Fastify, Hono and NestJS](deploy-express.md) (Node)
- [Next.js](deploy-nextjs.md) (Node)
- [Bun](deploy-bun.md)
- [FastAPI](deploy-fastapi.md) (Python)
- [Django](deploy-django.md) (Python)
- [Flask](deploy-flask.md) (Python)
- [Go](deploy-go.md) (`net/http`, Gin)

Moving an existing app? See [Migrate from Heroku](migrate-from-heroku.md),
[Migrate from Render](migrate-from-render.md),
[Migrate from Railway](migrate-from-railway.md) and
[Migrate from Fly.io](migrate-from-fly.md).

## What Gregale detects

| Framework | Detected from | Start command Gregale infers |
| --- | --- | --- |
| NestJS, Hono, Fastify, Express | the dependency in `package.json` | `npm run start:prod`, else `npm run start`, else `node` on `main`, `server.js`, `app.js` or `index.js` |
| FastAPI | `fastapi` in `requirements.txt`, `pyproject.toml`, `Pipfile` or `setup.py` | `uvicorn <module>:<app> --host 0.0.0.0 --port $PORT` |
| Django | `django` in the same files, or a `manage.py` | `gunicorn <project>.wsgi:application --bind 0.0.0.0:$PORT` |
| Flask | `flask` in the same files | `gunicorn <module>:<app> --bind 0.0.0.0:$PORT` |
| Go (`net/http`, Gin) | `go.mod` | the binary the build produces |

The package manager follows the lockfile (npm, pnpm, Yarn or Bun for Node;
pip, uv, Poetry or Pipenv for Python). With a `Dockerfile` in the project
root, Gregale builds that instead and uses its `CMD` and `EXPOSE`; see
[Container compatibility](container-compatibility.md).

Check what Gregale will do before anything is uploaded:

```bash
gregale deploy --dry-run
```

## The start command

The command an app runs is, in order of precedence:

1. `hosting.start` in `gregale.yaml`;
2. the `web:` line of a `Procfile`;
3. the start command the build detects;
4. the command in the table above, when the build found none.

`gregale deploy --dry-run` shows the command from step 4. When the build
detects a command of its own (step 3), that one runs instead, and the two can
differ: a Flask app without a `Procfile` can be started as `main:app`. For a
predictable deploy, write the command down, in either place:

```yaml
# gregale.yaml
hosting:
  start: gunicorn mysite.wsgi:application --bind 0.0.0.0:$PORT
```

```text
# Procfile
web: gunicorn mysite.wsgi:application --bind 0.0.0.0:$PORT
```

A start script that looks like a development server (`nodemon`, `vite`,
`next dev`, `tsx watch`, a `dev` script) gets a `development_start_command`
warning. Use the framework's production server.

## The listener

Every app must:

- listen on `0.0.0.0`, not `localhost` or `127.0.0.1`. Source that mentions
  either gets a `loopback_bind_possible` warning;
- read its port from the `PORT` environment variable, which Gregale sets for
  every app. An app listening on another port fails its deployment with
  `app_not_listening`, naming both ports.

To pin the port, set `hosting.port` in `gregale.yaml`; see
[Deployment overrides](deploy-overrides.md).

## Health checks

When the source defines one of `/healthz`, `/health`, `/readyz`, `/ready`,
`/livez` or `/live`, Gregale uses the first of those as the app's health
path, and a deployment goes live once it answers 2xx. Without one, a
deployment goes live once the port accepts connections. Name the path
explicitly with `hosting.health`:

```yaml
hosting:
  health: /healthz
```

Keep it cheap and free of side effects. A running app must keep answering
its health path; see [Deployment overrides](deploy-overrides.md) for how
Gregale restarts an instance that stops answering.

The check calls the app from inside the instance with the instance's
internal address as the `Host` header. An app that rejects unknown hosts,
such as Django with `ALLOWED_HOSTS`, must answer its health path before that
host check; see [Deploy a Django app](deploy-django.md#health-check).

Do not use `/` as the health path. Gregale can answer an app's public health
path at its edge without running the app, so with `/` visitors get
`{"status":"ok","source":"edge"}` instead of your page. Add a small route
such as `/healthz` instead. `gregale deploy --dry-run` and `gregale doctor` flag a
`/` health path as `health_path_root`.

## Runtime versions

With no version declared, Node apps build on Node 24 and Python apps on
Python 3.13. Declare major 22 in `.nvmrc` or `engines.node`, or 3.12 in
`.python-version`, `runtime.txt` or `Pipfile`, to get those instead. Any
other version still builds, but the app then carries its own runtime in its
app layer, which counts toward the plan's app layer limit (see
[Plans and pricing](plans.md)).

## Deploy and check

```bash
gregale deploy --name my-api
gregale app health my-api --explain
```

`gregale deploy` waits for the build, the readiness check and a smoke
request, then prints the app's URL. Add `--worktree` to include uncommitted
files. If the deployment fails, read why before deploying again:

```bash
gregale app health my-api --errors
gregale logs my-api
```

A new app can be private: its URL answers `401` until you allow anonymous
requests, and `gregale deploy` says so when it finishes. One command makes it
public:

```bash
gregale app update my-api --no-require-authn
```

It removes the API key requirement, including the bearer check on the public
URL that paid plans start with. Access you chose yourself (basic auth, an IP
allowlist or internal only) stays; the command says so, and
`--public-auth open` removes it.

See [Quickstart](quickstart.md) for testing a private app without making it
public.

## Configuration, secrets and databases

- Set configuration and secrets with `gregale env set --app my-api KEY=value`;
  they arrive as environment variables. See [Secrets](secrets.md).
- Add a managed database with
  `gregale add postgres --app my-api`. Its URL arrives as
  `DATABASE_URL`. See [Managed PostgreSQL](postgres.md).
- Run migrations as a release command: `release:` in the `Procfile` or
  `release.command` in `gregale.yaml`. It runs before the new version takes
  traffic, and a failure leaves the previous version serving. See
  [Safe releases](releases.md#releases-that-change-the-database).

The filesystem is ephemeral: keep state in a database or object storage, not
on local disk.
