# Container compatibility

Gregale's container contract is:

> A stateless Linux/amd64 OCI image that starts an HTTP server on
> `0.0.0.0:$PORT` can be deployed without rewriting it as a function.

Deploy an existing image directly:

```sh
gregale doctor --image registry.example.com/team/api@sha256:<digest>
gregale deploy --image registry.example.com/team/api@sha256:<digest>
```

Gregale preserves the OCI/Docker process model. Image metadata supplies the
entrypoint, command, environment, working directory, user, health check, stop
signal, and exposed ports. Deployment overrides can replace the start command,
port, or readiness settings. An explicit port wins; otherwise a single exposed
TCP port seeds the default.

This makes existing Go, Rust, Java, Spring, .NET, Node, Python, nginx, custom
binaries, and Docker applications portable. Images can come from a Dockerfile
or an OCI registry. A multi-platform index resolves to its Linux/amd64 image.

Images that do not share a Gregale-optimized base can use a self-contained
full-rootfs deployment. Paid plans enable that path automatically; Free plans
currently require an explicit opt-in. The resulting workload still uses the
same Firecracker isolation, snapshots, metering, routing, and scale-to-zero
lifecycle.

The portability boundary is intentionally small: Linux/amd64, a stateless HTTP
listener reachable on `0.0.0.0`, and a process that honors `PORT` (default
`8080`). The root filesystem is ephemeral. OCI volumes and host mounts do not
create durable storage, and privileged mode, host devices, host networking, and
the Docker socket are not supported.

Direct OCI images use TCP listener readiness by default; Gregale does not
invent a `/healthz` endpoint that the image never declared. An explicit
deployment health-path override selects HTTP readiness instead.

Gregale adds the managed infrastructure around that process: TLS, readiness,
logs and metrics, snapshots, autoscaling, and scale-to-zero.

## Randomness after a snapshot restore

A scaled-to-zero app wakes from a snapshot of its running process, so any
random generator the process held in memory is restored with it. The guest
kernel is reseeded on every wake, so anything that reads the kernel on each
call is always safe: `/dev/urandom`, `getrandom(2)`, Go `crypto/rand`, Python
`secrets`, `os.urandom` and `uuid.uuid4`, and Node `crypto.webcrypto`.

For Node and Python, Gregale also reseeds the process itself before a woken
instance serves traffic (ADR-222). It covers Node `crypto` (and the OpenSSL
state behind TLS), `crypto.randomUUID`, `crypto.randomInt` and `Math.random`,
and Python `random`, `ssl`, and numpy's global `numpy.random` functions. If a
process cannot confirm the reseed, the instance cold-boots instead of waking
from the snapshot.

Reseed these yourself, or avoid keeping them across a snapshot:

- generators in other runtimes, such as Go `math/rand`, Java `Random` and
  `SecureRandom`, Ruby `Random`, and PHP `mt_rand`;
- generator objects your code created before the snapshot, such as Python
  `random.Random()` or `numpy.random.default_rng()`;
- `Math.random` inside Node worker threads;
- sidecars that run from their own root filesystem.

Setting `GREGALE_RESTORE_RESEED=off` in the app's environment disables the
Node and Python reseed. Only do that for an app that never generates tokens,
keys, nonces or identifiers.
