# Deploy an Express app

Gregale deploys an Express, Fastify, Hono or NestJS app from its source: it
reads `package.json`, installs dependencies with the package manager your
lockfile names, and starts the app. What every framework shares is on
[Deploy a web framework](frameworks.md).

## Prepare the app

Listen on the port Gregale gives you, on every interface:

```js
import express from "express";

const app = express();

app.get("/healthz", (_req, res) => res.json({ ok: true }));
app.get("/", (_req, res) => res.json({ message: "hello" }));

app.listen(process.env.PORT || 3000, () => {
  console.log(`listening on ${process.env.PORT || 3000}`);
});
```

`app.listen(port)` without a host listens on every interface. Passing
`"localhost"` or `"127.0.0.1"` as the host makes the app unreachable. A
`/healthz` route is optional; with one, a deployment goes live only once it
answers 2xx.

Give `package.json` a `start` script that runs the production server:

```json
{
  "scripts": {
    "build": "tsc",
    "start": "node dist/server.js"
  }
}
```

Gregale runs `npm run start:prod` when that script exists, otherwise
`npm run start`, otherwise `node` on `main`, `server.js`, `app.js` or
`index.js`. A `start` script that runs `nodemon`, `tsx watch` or another
development server gets a `development_start_command` warning. For a
TypeScript or NestJS app, the start script must run the compiled output.

To skip inference, set the command in `gregale.yaml`:

```yaml
hosting:
  start: node dist/server.js
  health: /healthz
```

## Choose the Node version

With no declaration the app builds on Node 24. `"engines": {"node": "22"}` or
`22` in `.nvmrc` selects Node 22. See
[Runtime versions](frameworks.md#runtime-versions) for other versions.

## Deploy

```bash
gregale deploy --dry-run
gregale deploy --name my-api
```

`--dry-run` shows the detected framework, start command, port and health
path. The deploy prints the app's URL when it is live. If it fails:

```bash
gregale app health my-api --errors
gregale logs my-api
```

## Configuration and secrets

```bash
gregale env set --app my-api SESSION_SECRET="$SESSION_SECRET"
```

Values arrive in `process.env`. See [Secrets](secrets.md).

## Add PostgreSQL

```bash
gregale add postgres --app my-api --region <region>
```

The connection URL arrives as `DATABASE_URL`:

```js
import pg from "pg";

const pool = new pg.Pool({ connectionString: process.env.DATABASE_URL, max: 5 });
```

`DATABASE_URL` goes through a connection pooler in transaction mode, so
session state does not carry between transactions. Run migrations as a
release command with their own credential:

```bash
gregale postgres attach my-api-postgres my-api --access migration --env MIGRATION_DATABASE_URL
```

```text
# Procfile
release: npm run migrate
```

The migration script should connect with `MIGRATION_DATABASE_URL`, which only
the release command receives. See [Managed PostgreSQL](postgres.md#connect-an-app).

## Example repository

[gregale-dev/node-api-worker](https://github.com/gregale-dev/node-api-worker)
is an Express API and a private worker with a durable job queue in
PostgreSQL, deployed as one project from its Compose file with
`gregale deploy --project`. The API wakes the worker only when there's work,
so both stop when idle. See [Multi-service apps](multi-service-apps.md) for
how the Compose file maps to Gregale.
