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

Persistent container disks are outside Gregale's service contract. Store durable
application state in object storage or an external database. Snapshot reuse is a
startup optimization and does not guarantee filesystem persistence across
instances, cold fallback, redeployment, or node failure. The ephemeral disk
allowance includes application content and filesystem overhead, rather than
guaranteeing that amount of free scratch space; see [ADR-158](adr/158-ephemeral-disk-boundary.md).

Direct OCI images use TCP listener readiness by default; Gregale does not
invent a `/healthz` endpoint that the image never declared. An explicit
deployment health-path override selects HTTP readiness instead.

Gregale adds the managed infrastructure around that process: TLS, readiness,
logs and metrics, snapshots, autoscaling, and scale-to-zero.

## Support boundaries

| Concern | Current contract | Qualification boundary |
|---|---|---|
| Image platform | Linux/amd64; compatible child selected from an OCI or Docker index | Other architectures and operating systems are rejected. |
| Process | Image ENTRYPOINT + CMD, environment, working directory, and user | Metadata inspection cannot prove executables or users exist in the layers. Numeric or named `USER user:group` is preserved; explicit missing identities fail launch. Supplementary memberships are not imported. |
| Main listener | Explicit deployment port, otherwise one exposed TCP port, otherwise 8080 | Multiple TCP declarations do not select the main port. Bind to `0.0.0.0`. |
| Additional listeners | Declared TCP listeners use `<slug>--port-<name>.<apps-domain>` selectors through the gateway | These are HTTP gateway selectors. Raw TCP uses separately configured app-owned listeners; UDP uses the separate opt-in listener service below. |
| Raw TCP ingress | Opt-in public edge binds stable app-owned TCP listener ports and forwards bytes through admission and vmmd; optional per-listener TLS termination | Disabled by default; requires gateway/firewall rollout. TLS defaults to passthrough; termination requires verified app-owned hostname and provisioned edge bundle. Native TLS acceptance remains pending. See [rollout](ops/tcp-ingress.md). |
| Raw UDP ingress | Opt-in public edge reserves app-owned UDP endpoints and forwards framed datagrams through schedd and VMMD | Disabled by default; requires IPv4 source CIDRs and matching firewall rollout. Local API/storage/socket tests pass; native cold/restore acceptance is pending. See [ADR-389](adr/389-public-udp-ingress.md). |
| Host ports | Durable per-node TCP/UDP lease allocation | A lease does not bind a socket or enable direct ingress. |
| Readiness | TCP by default for direct images; explicit health path selects HTTP | Image HEALTHCHECK metadata is separate from verifying public-route readiness. |
| Image healthchecks | CMD/CMD-SHELL polling with image identity, environment, working directory, and exact nanosecond timing; StartInterval is retained | CMD-SHELL requires the image to contain its shell. Qualification must prove execution and reporting, including companion deployments and restore. |
| Filesystem | Compatible shared-base layering or full-rootfs fallback; ephemeral writable state | VOLUME does not provision persistent storage; no host mounts or Docker socket. |
| Lifecycle | Request, service, worker, and job modes with validated restart policies | Free is request-only; quotas and availability remain plan-controlled. |
| Companions | Separate image roots and resource controls, shared guest kernel and loopback | Use separate deployments for mutually untrusted workloads. |
| Snapshot restore | Restorable cache with cold-boot fallback; optional lifecycle callbacks | External connections and durable state require application recovery handling. |

Availability is defined by the [capability matrix](capabilities.md), not by this
compatibility table. In particular, container deployment is beta while
companions and worker pools are preview.

## Image preflight

During self-contained full-rootfs assembly, Gregale checks the effective startup
command after applying all image layers and deployment command overrides. An
explicit command path must resolve to a regular file with an execute bit;
relative paths resolve from the image working directory. Shell-form startup
commands therefore require their selected shell (usually `/bin/sh`) in the
image. Missing commands, invalid symlinks, directories, and non-executable files
fail the deployment with `image_manifest_invalid` before ext4 publication or VM
boot. The failure identifies the command without exposing arguments or
environment values.

For bare commands, assembly uses the image/deployment `PATH` plus the current
scoped app environment, with the same absolute-directory lookup as guest-init.
A sealed `PATH` or unavailable runtime environment defers that lookup to the
guest. Assembly never decrypts secrets or copies runtime `PATH` into the image.
Later environment changes remain subject to guest launch validation.
Commands under guest-provided mounts (`/dev`, `/proc`, `/sys`, `/tmp`, and
companion roots), including image symlinks to those paths, also defer to launch
because their contents are supplied or replaced at boot.

This check does not run image code or prove ELF/script interpreter compatibility,
user permissions, healthcheck execution, shell command contents, or readiness.
It applies to the complete full-rootfs main image; the shared-base upper-layer
path cannot prove whether a command exists in its base and retains its guest
validation. The metadata-only doctor command below retains its fast path.

`gregale doctor --image REF --json` reports `image.serving_port` as inferred
from image metadata before deployment overrides. Human output shows the same
port. A listener finding warns when multiple TCP ports leave the default
ambiguous or when only UDP ports are declared. `--strict` makes those warnings
fail the preflight; normal mode reports them without rejecting the image.
An image exceeding the workload listener cap fails runtime-contract validation.
Declared volume paths appear in `image.volume_paths` and human output, with an
explicit warning that they remain ephemeral and do not provision durable storage.
UDP-only images receive guidance for configuring app-owned UDP endpoints and the
operator source-CIDR rollout; a declaration alone does not enable ingress or
provide the main HTTP readiness listener.

Preflight does not download layers, execute image commands, prove that the
server binds its port, or validate account policy. A successful metadata check
is not runtime qualification. See the [container qualification procedure](container-qualification.md)
for the separate offline and native acceptance gates.
