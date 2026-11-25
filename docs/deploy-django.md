# Deploy a Django app

Gregale deploys a Django project from its source: it finds `django` in your
dependencies (or a `manage.py`), installs them, and serves the project's WSGI
application with Gunicorn. What every framework shares is on
[Deploy a web framework](frameworks.md).

## Prepare the project

Add Gunicorn to the dependencies, since Gregale starts the app with it:

```text
# requirements.txt
django
gunicorn
dj-database-url
psycopg[binary]
```

Gregale finds the project's `wsgi.py` and infers:

```text
gunicorn mysite.wsgi:application --bind 0.0.0.0:$PORT
```

If it cannot find one it falls back to `app.wsgi` and warns. To skip
inference, set the command in a `Procfile` or `gregale.yaml`:

```text
# Procfile
web: gunicorn mysite.wsgi:application --bind 0.0.0.0:$PORT --workers 2
release: python manage.py migrate --noinput
```

Never deploy `manage.py runserver`; it is a development server.

## Settings

Read configuration from the environment in `settings.py`:

```python
import os

import dj_database_url

SECRET_KEY = os.environ["DJANGO_SECRET_KEY"]
DEBUG = os.environ.get("DJANGO_DEBUG") == "1"
ALLOWED_HOSTS = os.environ.get("DJANGO_ALLOWED_HOSTS", "").split(",")

# Release commands receive MIGRATION_DATABASE_URL; serving instances do not.
DATABASES = {
    "default": dj_database_url.parse(
        os.environ.get("MIGRATION_DATABASE_URL") or os.environ["DATABASE_URL"],
        conn_max_age=0,
    )
}
DATABASES["default"]["DISABLE_SERVER_SIDE_CURSORS"] = True
```

- `DJANGO_ALLOWED_HOSTS` lists the app's host names: its default host
  `<app>.gregale.app` and any [custom domain](domains.md). Set it
  before the first deploy. Every deploy ends with a request to the default
  host, and Django answers `400 Bad Request` to a host it does not allow,
  which fails the deploy. Health checks do not use these names; see
  [Health check](#health-check).
- `DATABASE_URL` goes through a connection pooler in transaction mode.
  `DISABLE_SERVER_SIDE_CURSORS` and `conn_max_age=0` are the settings Django
  needs behind one.

Set the values once:

```bash
gregale env set --app my-site DJANGO_SECRET_KEY="$(python -c 'import secrets; print(secrets.token_urlsafe(50))')"
gregale env set --app my-site DJANGO_ALLOWED_HOSTS="my-site.gregale.app,www.example.com"
```

The app must exist before `gregale env set`: reserve it first with
`gregale deploy --create-only --name my-site`, or hand the values to the
first deploy in a `KEY=VALUE` file with `--secrets-file PATH` (keep that file
out of the repository).

## Database and migrations

```bash
gregale add postgres --app my-site --region <region>
gregale postgres attach my-site-postgres my-site --access migration --env MIGRATION_DATABASE_URL
```

The first command creates a database and binds it as `DATABASE_URL`, which
can read and write data but not change the schema. The second gives the
release command a `migration` credential that can. With the `release:` line
above, `python manage.py migrate` runs before each new version takes traffic;
if it fails, the previous version keeps serving. See
[Managed PostgreSQL](postgres.md#connect-an-app) and
[Safe releases](releases.md#releases-that-change-the-database).

## Static files

The build does not run `collectstatic`, and the app's own directory is
read-only while it runs, so a `STATIC_ROOT` inside the project fails with a
permission error. Collect into a writable directory when the app starts, and
serve the files from the app with [WhiteNoise](https://whitenoise.readthedocs.io/):

```python
STATIC_ROOT = os.environ.get("STATIC_ROOT", "/tmp/staticfiles")
```

```text
web: python manage.py collectstatic --noinput && gunicorn mysite.wsgi:application --bind 0.0.0.0:$PORT
```

The filesystem is ephemeral and there is no shared disk: user uploads belong
in object storage, not `MEDIA_ROOT`.

## Health check

Gregale checks the health path from inside the instance, when a new
version starts and while it runs. The check's `Host` header is the
instance's internal address, not `<app>.gregale.app`, and Django answers a
host missing from `ALLOWED_HOSTS` with `400 Bad Request` before any view
runs. If the health path is a view in `urls.py`, every check fails and the
deploy fails with `app_startup_timeout`. Answer the health path in a
middleware that runs before Django checks the host:

```python
# mysite/health.py
from django.http import HttpResponse


class HealthCheckMiddleware:
    def __init__(self, get_response):
        self.get_response = get_response

    def __call__(self, request):
        if request.path == "/healthz":
            return HttpResponse("ok")
        return self.get_response(request)
```

Put it first in `MIDDLEWARE`, above `SecurityMiddleware`:

```python
MIDDLEWARE = [
    "mysite.health.HealthCheckMiddleware",
    "django.middleware.security.SecurityMiddleware",
    # ... the rest of the project's middleware
]
```

Gregale finds the `/healthz` comparison in the source and uses it as the
health path. Do not add the internal address to `ALLOWED_HOSTS`: it is not
stable. Without a health path Gregale only waits for the port to accept
connections.

## HTTPS

Gregale terminates TLS and passes requests to the app with
`X-Forwarded-Proto: https`. To redirect plain HTTP with Django, trust that
header:

```python
SECURE_PROXY_SSL_HEADER = ("HTTP_X_FORWARDED_PROTO", "https")
SECURE_SSL_REDIRECT = True
```

Health checks call the app over plain HTTP. The health middleware answers
them before `SecurityMiddleware` can redirect, so nothing needs exempting.

## Choose the Python version

With no declaration the project builds on Python 3.13. `3.12` in
`.python-version` or `runtime.txt` selects Python 3.12. See
[Runtime versions](frameworks.md#runtime-versions) for other versions.

## Deploy

```bash
gregale deploy --dry-run
gregale deploy --name my-site
```

If the deployment fails:

```bash
gregale app health my-site --errors
gregale logs my-site
```

A `DisallowedHost` error naming `<app>.gregale.app` or your domain means
that host is missing from `DJANGO_ALLOWED_HOSTS`. One naming an IP address
comes from the health check: put the health middleware first in
`MIDDLEWARE`, as in [Health check](#health-check).
