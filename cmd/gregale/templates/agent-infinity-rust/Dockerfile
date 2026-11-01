# Binary runtime (ADR-987): build one static executable and name it with
# ENTRYPOINT. Gregale extracts that file to /app/handler and runs it on its own
# runtime base, so only the executable leaves this build.
FROM rust:1.90-alpine AS build
# Alpine's Rust targets musl, which links statically by default. The TLS
# crates (rustls with ring and aws-lc) build their C parts with these.
RUN apk add --no-cache musl-dev cmake make perl
WORKDIR /src
COPY Cargo.toml Cargo.lock ./
COPY src ./src
RUN cargo build --release --locked

FROM scratch
COPY --from=build /src/target/release/handler /app/handler
ENTRYPOINT ["/app/handler"]
