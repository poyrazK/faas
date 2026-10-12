# function-rust

A Rust function on Gregale's `binary` runtime. The runtime runs one statically
linked Linux executable in any compiled language; this template builds it
from Rust.

How it fits together:

1. The `Dockerfile` builds a static musl binary and names it with
   `ENTRYPOINT ["/app/handler"]`. Gregale builds that Dockerfile and keeps
   only the executable, so it must live inside `/app` and be statically
   linked.
2. The runner (the HTTP server inside the microVM, listening on `:8080`)
   talks to the executable over stdin/stdout: one §4.9 request envelope per
   line in, one response envelope per line out. `src/main.rs` implements
   that loop; put your logic in `handle`.
3. The program speaks the persistent protocol, so the runner starts it once
   per instance and reuses it for every request. After a snapshot restore
   the runner starts a fresh process, so restored instances never share
   random state.

stdout carries only response envelopes. Log with `eprintln!`; stderr reaches
`gregale logs`.

## Deploy

```
gregale deploy --template function-rust --name <slug>
```

or, from an initialized project:

```
gregale deploy
```

`gregale.yaml` records `runtime: binary`, so the CLI builds from the
Dockerfile.

## Local test

```
cargo build --release
printf '{"method":"GET","path":"/hello"}\n' | ./target/release/handler
```

prints one response envelope. Set `FAAS_PERSISTENT_WORKER=1` and send several
lines to see the persistent mode: a ready line, then one response per request.
