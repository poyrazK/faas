# Deploy a Rust app

Gregale builds Node, Python and Go from source, but not Rust. A Rust web
service deploys from a `Dockerfile` that compiles it with Cargo and copies
the binary into a small runtime image. Axum, Actix Web and other servers
work the same way. What every framework shares is on
[Deploy a web framework](frameworks.md).

## Prepare the app

Listen on `0.0.0.0` and the port in `PORT`. With Axum:

```rust
use axum::{Router, routing::get};

#[tokio::main]
async fn main() {
    let app = Router::new()
        .route("/healthz", get(|| async { "ok" }))
        .route("/", get(|| async { "hello" }));

    let port = std::env::var("PORT").unwrap_or_else(|_| "8080".into());
    let listener = tokio::net::TcpListener::bind(format!("0.0.0.0:{port}"))
        .await
        .unwrap();
    axum::serve(listener, app).await.unwrap();
}
```

Do not bind `127.0.0.1` or `localhost`: the app would not be reachable.
With Actix Web, `HttpServer::new(...).bind(("0.0.0.0", port))` does the
same.

## Dockerfile

Add a `Dockerfile` at the project root. Replace `my-api` with the package
name in `Cargo.toml`:

```dockerfile
FROM rust:1-slim-bookworm AS build
WORKDIR /src
COPY Cargo.toml Cargo.lock ./
COPY src src
RUN cargo build --release --locked
FROM gcr.io/distroless/cc-debian12
COPY --from=build /src/target/release/my-api /app/my-api
EXPOSE 8080
CMD ["/app/my-api"]
```

- Commit `Cargo.lock`; `--locked` builds exactly what it lists.
- `distroless/cc` has the C libraries a Rust binary links against and no
  shell or package manager.
- `gregale deploy` leaves `target/` out of the upload.
- Each build gets 2 vCPU, 2048 MB of memory and 15 minutes; see
  [Builds and framework detection](build.md).

Gregale runs the image's `CMD`. To name the health path explicitly:

```yaml
# gregale.yaml
hosting:
  health: /healthz
```

## Deploy

From the project root, preview what Gregale detects, then deploy:

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
through a connection pooler in transaction mode. sqlx's prepared statements
work through it. With sqlx:

```rust
let pool = sqlx::postgres::PgPoolOptions::new()
    .max_connections(5)
    .connect_lazy(&std::env::var("DATABASE_URL")?)?;
```

`connect_lazy` lets the app start and answer health checks before it
reaches the database.

Run migrations as a release command and give it its own credential, which
only the release command receives:

```bash
gregale postgres attach my-api-postgres my-api --access migration --env MIGRATION_DATABASE_URL
```

With sqlx, the binary can run its own migrations. `sqlx::migrate!()` embeds
the `migrations/` folder at compile time, so copy it before `cargo build`
(`COPY migrations migrations`), and run them when the binary is started with
a `migrate` argument:

```rust
if std::env::args().nth(1).as_deref() == Some("migrate") {
    let url = std::env::var("MIGRATION_DATABASE_URL")?;
    let pool = sqlx::postgres::PgPoolOptions::new()
        .max_connections(1)
        .connect(&url)
        .await?;
    sqlx::migrate!().run(&pool).await?;
    return Ok(());
}
```

```yaml
# gregale.yaml
release:
  command: /app/my-api migrate
```

The release command runs before the new version takes traffic; if it
fails, the previous version keeps serving. See
[Managed PostgreSQL](postgres.md#connect-an-app).
