# Deploy a Bun app

Gregale deploys a Bun app from its source. A `bun.lock` makes the build
install Bun, run `bun install --frozen-lockfile`, and start the app with
`bun run start`. What every framework shares is on
[Deploy a web framework](frameworks.md).

A Bun app runs on every plan, Free included. The app runs on Gregale's Node 24
base and keeps only Bun's own install (about 90 MB) next to your code and
dependencies, so a small app is about 95 MB. The Free plan caps the app layer
at 256 MB, which leaves about 160 MB for `node_modules`. See
[App layer](build.md#app-layer).

This holds when the start command runs `bun` and none of the scripts it
reaches call `npm`, `npx`, `yarn`, `pnpm`, or a script runner such as
`npm-run-all`, `run-p` or `concurrently`. A start that breaks either rule
keeps the whole toolchain, Node included, and does not fit Free. Use
`bun run <script>` or `bunx <tool>` in place of those tools.

## Prepare the app

Listen on the port Gregale gives you, on every interface:

```ts
// index.ts
import { Hono } from "hono";

const app = new Hono();

app.get("/healthz", (c) => c.json({ ok: true }));
app.get("/", (c) => c.json({ message: "hello" }));

Bun.serve({
  port: Number(process.env.PORT) || 3000,
  hostname: "0.0.0.0",
  fetch: app.fetch,
});
```

Give `package.json` a `start` script and commit `bun.lock`:

```json
{
  "scripts": {
    "dev": "bun --watch index.ts",
    "start": "bun index.ts"
  }
}
```

Bun runs TypeScript directly, so the app needs no build step. With a
`/healthz` route in the source,
Gregale uses it as the health path and a deployment goes live only once it
answers 2xx.

## Deploy

```bash
gregale deploy --dry-run
gregale deploy --name my-api
```

`--dry-run` shows `framework=node`, the start command `bun run start` and the
health path. The deploy prints the app's URL when it is live. If it fails:

```bash
gregale app health my-api --errors
gregale logs my-api
```

## Random numbers after a wake

A scaled-to-zero app wakes from a snapshot of its running process. Gregale
reseeds Node and Python processes before a woken instance serves traffic,
but not Bun, and logs a warning on each wake that names it. In Bun:

- `Math.random()` repeats: every wake from the same snapshot returns the same
  sequence (measured);
- `crypto.randomUUID()` and `crypto.getRandomValues()` return new values on
  every wake.

Use `crypto` for IDs, tokens and anything else that must not repeat. See
[Randomness after a snapshot restore](container-compatibility.md#randomness-after-a-snapshot-restore).

## Configuration and secrets

```bash
gregale env set --app my-api SESSION_SECRET="$SESSION_SECRET"
```

Values arrive in `process.env` and `Bun.env`. See [Secrets](secrets.md).

## Add PostgreSQL

```bash
gregale add postgres --app my-api --region <region>
```

The connection URL arrives as `DATABASE_URL`. It goes through a connection
pooler in transaction mode, so session state does not carry between
transactions. See [Managed PostgreSQL](postgres.md#connect-an-app), and
[Deploy an Express app](deploy-express.md#add-postgresql) for running
migrations as a release command.
