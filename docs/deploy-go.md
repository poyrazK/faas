# Deploy a Go app

Gregale deploys a Go web service from its source: it finds `go.mod`, builds
the module with the Go release its `go` line names, and runs the binary.
`net/http`, Gin and other routers work the same way. What every framework
shares is on [Deploy a web framework](frameworks.md).

## Prepare the app

```go
package main

import (
	"log"
	"net/http"
	"os"
)

func main() {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte("ok"))
	})
	mux.HandleFunc("GET /", func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte("hello"))
	})

	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}
	log.Fatal(http.ListenAndServe(":"+port, mux))
}
```

`":" + port` listens on every interface. Do not bind `localhost:` or
`127.0.0.1:`. With Gin, `router.Run(":" + port)` does the same.

With the main package at the module root, nothing else is needed: the build
compiles it to `./out` and runs that. For another layout (for example
`cmd/server`), check what `gregale deploy --dry-run` finds; if it finds no
runnable start command, add a `Dockerfile` (see
[Container compatibility](container-compatibility.md)).

A `Procfile` from Heroku's Go buildpack names a binary that buildpack
installs (`web: my-app`); on Gregale that command does not exist and the app
crashes at start. Point the start command at the build's binary:

```yaml
# gregale.yaml
hosting:
  start: ./out
```

To name the health path explicitly:

```yaml
# gregale.yaml
hosting:
  health: /healthz
```

## Go version

The `go` line in `go.mod` picks the Go release the build uses. See
[Builds and framework detection](build.md).

## Deploy

From the module's root, preview what Gregale detects, then deploy the Go
HTTP API:

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
gregale env set --app my-api API_KEY="$API_KEY"
gregale add postgres --app my-api
```

Values arrive as environment variables; the database URL as `DATABASE_URL`,
through a connection pooler in transaction mode. With pgx:

```go
pool, err := pgxpool.New(ctx, os.Getenv("DATABASE_URL"))
```

Run migrations as a release command (`release:` in a `Procfile`, or
`release.command` in `gregale.yaml`) that your build ships, and give it its
own credential, which only the release command receives:

```bash
gregale postgres attach my-api-postgres my-api --access migration --env MIGRATION_DATABASE_URL
```

See [Managed PostgreSQL](postgres.md#connect-an-app).
