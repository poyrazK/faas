# Deploy a FastAPI app

Gregale deploys a FastAPI app from its source: it finds `fastapi` in your
dependencies, installs them, and starts the app with Uvicorn. What every
framework shares is on [Deploy a web framework](frameworks.md).

To check an existing repository and verify its exact release through MCP,
follow the [FastAPI agent quickstart](agents.md#agent-quickstart-for-an-existing-fastapi-repo).

## Prepare the app

```python
# main.py
from fastapi import FastAPI

app = FastAPI()


@app.get("/healthz")
def healthz():
    return {"ok": True}


@app.get("/")
def root():
    return {"message": "hello"}
```

List the server with the app's dependencies, since Gregale starts it with
`uvicorn`:

```text
# requirements.txt
fastapi
uvicorn
```

`pyproject.toml`, `Pipfile` and `setup.py` work too; the lockfile picks the
package manager (`uv.lock`, `poetry.lock`, `Pipfile.lock`, else pip).

Gregale looks for the module that creates `FastAPI()` and infers:

```text
uvicorn main:app --host 0.0.0.0 --port $PORT
```

If it cannot find one it falls back to `app:app` and warns. To skip
inference, set the command:

```yaml
# gregale.yaml
hosting:
  start: uvicorn main:app --host 0.0.0.0 --port $PORT
  health: /healthz
```

Keep `--host 0.0.0.0` and `--port $PORT`: Uvicorn's defaults
(`127.0.0.1:8000`) are unreachable. Start the server directly rather than
through `uv run` or `poetry run`; the package manager is not part of the
running app.

For more than one worker process, use Gunicorn with Uvicorn workers and add
`gunicorn` to the dependencies:

```text
web: gunicorn main:app -k uvicorn.workers.UvicornWorker --bind 0.0.0.0:$PORT --workers 2
```

## Choose the Python version

With no declaration the app builds on Python 3.13. `3.12` in
`.python-version` or `runtime.txt` selects Python 3.12. See
[Runtime versions](frameworks.md#runtime-versions) for other versions.

## Deploy

```bash
gregale deploy --dry-run
gregale deploy --name my-api
```

If the deployment fails:

```bash
gregale app health my-api --errors
gregale logs my-api
```

## Configuration and secrets

```bash
gregale env set --app my-api API_KEY="$API_KEY"
```

Values arrive as environment variables (`os.environ["API_KEY"]`). See
[Secrets](secrets.md).

## Add PostgreSQL

```bash
gregale add postgres --app my-api --region <region>
```

The connection URL arrives as `DATABASE_URL`. It goes through a connection
pooler in transaction mode: keep the app's pool small and do not rely on
session state between transactions. With psycopg 3:

```python
import os
from psycopg_pool import ConnectionPool

pool = ConnectionPool(os.environ["DATABASE_URL"], max_size=5)
```

Run migrations (for example Alembic) as a release command with their own
credential, which only the release command receives:

```bash
gregale postgres attach my-api-postgres my-api --access migration --env MIGRATION_DATABASE_URL
```

```text
# Procfile
web: uvicorn main:app --host 0.0.0.0 --port $PORT
release: alembic upgrade head
```

Point the migration tool at `MIGRATION_DATABASE_URL`. A failed release
command leaves the previous version serving. See
[Managed PostgreSQL](postgres.md#connect-an-app).

## Example repository

[gregale-dev/fastapi-postgres-jobs](https://github.com/gregale-dev/fastapi-postgres-jobs)
is a complete FastAPI app with a durable job queue in PostgreSQL, with no
Redis and no always-on worker. Its `gregale.yaml` creates the database and
runs the migration as the release command. Clone it, or create your own
repository from it as a GitHub template.
