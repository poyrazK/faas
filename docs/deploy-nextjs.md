# Deploy a Next.js app

Gregale deploys a Next.js app from its source as a Node app: it installs
dependencies with the package manager your lockfile names, runs your `build`
script (`next build`), and starts your `start` script (`next start`). What
every framework shares is on [Deploy a web framework](frameworks.md).

The app runs as a server, so server components, route handlers, server
actions and middleware all work.

A Next.js app needs the Hobby plan or larger. Even a small app builds to about
340 MB, mostly `node_modules`, and the Free plan caps the app layer at 256 MB;
a larger build fails with `app_layer_too_large`. See
[App layer](build.md#app-layer) and [Plans and pricing](plans.md).

## Prepare the app

Keep the scripts `create-next-app` writes:

```json
{
  "scripts": {
    "dev": "next dev",
    "build": "next build",
    "start": "next start"
  }
}
```

`next start` reads the `PORT` Gregale sets and listens on every interface, so
it needs no flags. A `start` script that runs `next dev` gets a
`development_start_command` warning; deploy the production server.

Add a health route. Gregale cannot find route handlers in Next.js folders, so
name it in `gregale.yaml`:

```js
// app/healthz/route.js
export function GET() {
  return Response.json({ ok: true });
}
```

```yaml
# gregale.yaml
hosting:
  health: /healthz
```

Without one, a deployment goes live once the port accepts connections. Do not
use `/` as the health path; see [Health checks](frameworks.md#health-checks).

## Deploy

```bash
gregale deploy --dry-run
gregale deploy --name my-site
```

`--dry-run` shows `framework=node` and the start command `npm run start`.
`.next` and `node_modules` are not uploaded: the build makes them. The
deploy prints the app's URL when it is live. If it fails:

```bash
gregale app health my-site --errors
gregale logs my-site
```

A new app can be private, answering `401` until you allow anonymous requests.
A public website needs:

```bash
gregale app update my-site --no-require-authn
```

The build runs in a microVM with 2048 MB of memory. A large app whose
`next build` runs out of memory fails with a resource failure; see
[Builds and framework detection](build.md#memory).

## Make the app smaller

`output: "standalone"` makes `next build` copy only the files the server
needs. Build it with a Dockerfile so the image keeps only that output:

```js
// next.config.mjs
export default { output: "standalone" };
```

```dockerfile
FROM node:24-alpine AS build
WORKDIR /app
COPY package.json package-lock.json ./
RUN npm ci
COPY . .
RUN npm run build

FROM node:24-alpine
WORKDIR /app
ENV NODE_ENV=production
ENV HOSTNAME=0.0.0.0
COPY --from=build /app/.next/standalone ./
COPY --from=build /app/.next/static ./.next/static
EXPOSE 3000
CMD ["node", "server.js"]
```

If the app has a `public` directory, also copy it:
`COPY --from=build /app/public ./public`. Add a `.dockerignore` with
`node_modules` and `.next`. The standalone server reads `PORT`, and
`HOSTNAME=0.0.0.0` makes it listen on every interface rather than on the
machine's host name.

A small app built this way is about 230 MB instead of 340 MB, which leaves
room for larger dependencies. On Hobby, an image built from a Dockerfile like
this one is capped at 256 MB. See
[Container compatibility](container-compatibility.md) for how Gregale builds a
Dockerfile.

## Environment variables

```bash
gregale env set --app my-site API_TOKEN="$API_TOKEN"
```

Values set this way arrive in `process.env` when the server runs. See
[Secrets](secrets.md). Next.js copies `NEXT_PUBLIC_` variables into the
browser bundle when it builds, so they are not read at runtime: commit them in
`.env.production`. They are public by design; never put a secret in one.

## Add PostgreSQL

```bash
gregale add postgres --app my-site --region <region>
```

The connection URL arrives as `DATABASE_URL`, for server components, route
handlers and server actions. Each instance of the app holds its own pool, so
keep it small (`max: 5` with `pg`). See
[Managed PostgreSQL](postgres.md#connect-an-app), and
[Deploy an Express app](deploy-express.md#add-postgresql) for running
migrations as a release command.

## Caching and images

The filesystem is ephemeral, and each instance has its own. Pages Next.js
regenerates (ISR) and optimized images are cached on that instance's disk and
built again on a new one. Serve large static assets from
[object storage](storage.md) instead of `public/`.
