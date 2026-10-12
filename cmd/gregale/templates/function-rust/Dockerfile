# Binary runtime (ADR-987): build one static executable and name it with
# ENTRYPOINT. Gregale extracts that file to /app/handler and runs it on its own
# runtime base, so only the executable leaves this build.
FROM rust:1.90-alpine AS build
RUN apk add --no-cache musl-dev
WORKDIR /src
COPY Cargo.toml Cargo.lock ./
COPY src ./src
# Alpine's Rust targets musl, which links statically by default.
RUN cargo build --release --locked

FROM scratch
COPY --from=build /src/target/release/handler /app/handler
ENTRYPOINT ["/app/handler"]
