# Deploy a Flask app

Gregale deploys a Flask app from its source: it finds `flask` in your
dependencies, installs them, and serves the app with Gunicorn. What every
framework shares is on [Deploy a web framework](frameworks.md).

## Prepare the app

```python
# app.py
from flask import Flask, jsonify

app = Flask(__name__)


@app.get("/healthz")
def healthz():
    return jsonify(ok=True)


@app.get("/")
def root():
    return jsonify(message="hello")
```

Add Gunicorn to the dependencies, since Gregale starts the app with it:

```text
# requirements.txt
flask
gunicorn
```

Set the start command yourself. `gregale deploy --dry-run` shows the command
Gregale infers from the module that creates `Flask(...)` (here
`gunicorn app:app`), but a source build without a `Procfile` can start
`gunicorn main:app` instead, which fails with
`ModuleNotFoundError: No module named 'main'` unless the app lives in
`main.py`. A `Procfile` line settles it:

```text
# Procfile
web: gunicorn app:app --bind 0.0.0.0:$PORT
```

For an app factory or more workers:

```text
web: gunicorn "myapp:create_app()" --bind 0.0.0.0:$PORT --workers 2
```

Never deploy `flask run` or `app.run()`; they start Flask's development
server.

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

## Configuration, secrets and PostgreSQL

```bash
gregale env set --app my-api SECRET_KEY="$SECRET_KEY"
gregale add postgres --app my-api
```

Values arrive as environment variables; the database URL as `DATABASE_URL`,
through a connection pooler in transaction mode. With Flask-SQLAlchemy, keep
the pool small:

```python
import os

app.config["SQLALCHEMY_DATABASE_URI"] = os.environ["DATABASE_URL"]
app.config["SQLALCHEMY_ENGINE_OPTIONS"] = {"pool_size": 5, "pool_pre_ping": True}
```

SQLAlchemy needs a `postgresql+psycopg://` URL to use psycopg 3; replace the
scheme when you read it if that is the driver you install.

Run migrations (for example Flask-Migrate) as a release command with their
own credential:

```bash
gregale postgres attach my-api-postgres my-api --access migration --env MIGRATION_DATABASE_URL
```

```text
# Procfile
web: gunicorn app:app --bind 0.0.0.0:$PORT --workers 2
release: flask db upgrade
```

Point the migration at `MIGRATION_DATABASE_URL`, which only the release
command receives. See [Managed PostgreSQL](postgres.md#connect-an-app).
