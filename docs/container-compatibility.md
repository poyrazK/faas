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
