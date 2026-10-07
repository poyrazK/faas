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

For an image with TCP readiness and no HTTP health path, the post-readiness
public smoke checks candidate route connectivity at `/`. A candidate-authored
2xx, 3xx, 401, 403, or 404 response can verify connectivity; an API does not
need to implement a successful root route. Gateway errors, missing candidate
response evidence, a different revision, 429, and 5xx still fail verification.
An explicit HTTP health path (including `/`) continues to require 2xx.

The deployment's `hosting_receipt.smoke.verification` records `http_health`
or `route_connectivity`, alongside the actual response status.
`authentication: platform_challenge` means the platform probe bypassed Gregale
customer access gates; it does not prove anonymous access or bypass
application-owned authentication. Connectivity probes record redirects without
following them. Roll out the gateway response-proof support before the new
imaged verifier; older gateways cannot satisfy the connectivity check.

Both candidate verification contracts require an authenticated candidate
response, including explicit HTTP health checks. A deployment header alone,
gateway error, or missing/invalid response proof cannot verify the candidate.
Candidate redirects are never followed; HTTP health requires a proven 2xx on
the configured path.

If challenge publication, the public gateway response, or the verification
transport remains unavailable, the candidate stays `snapshotting` and retries
through the durable notification outbox. A transport failure does not establish
whether the app or platform caused it. The existing serving deployment keeps
traffic. Recovery progress is
available in `stage_state.hosting_verification`: `attempts`, `deadline_at`,
`last_error_code`, and `retry_not_before` (the earliest eligible retry, not a
promised delivery time). `last_error_code` distinguishes publication, gateway,
transport, missing-proof and wrong-deployment failures. The five-minute
recovery window survives imaged restarts and changes of outage reason. If it
expires, the deployment fails with `deployment_verification_unavailable`;
inspect deployment and gateway diagnostics before retrying. A proven candidate
response with an unhealthy status retains `deployment_smoke_failed`.

Roll out response-proof support to all gateways before enabling this verifier
for HTTP health checks; an older gateway cannot verify a candidate.

Gregale adds the managed infrastructure around that process: TLS, readiness,
logs and metrics, snapshots, autoscaling, and scale-to-zero.

## Support boundaries

Compose project services may use prebuilt `image:` references without a build
context. Stateless images join the deployment and dependency graph; database
and cache images remain managed-resource requirements. Scan plans display the
image declaration, and the image worker resolves tags to immutable references
before materialization. The first published TCP container target selects the
main port, and Compose commands retain the image ENTRYPOINT. Image workloads
return a deployment ID without a build ID. Source-defined operations on these
workloads currently require an atomic image admission path and are rejected.
After CI pushes an image, the [image-published trigger](image-published-deployments.md)
deploys its exact digest with durable per-workload and scope deduplication.
New project image deployments also capture their accepted Compose CMD contract;
queued images and retries retain it after app configuration changes. See
[ADR-640](adr/640-frozen-project-image-commands.md). Historical deployments without
that record keep their existing command behavior.

Compose `depends_on: {backend: {condition: service_healthy}}` now holds the
dependent workload's initial release until the captured backend deployment is
live with traffic in the same environment. A failed dependency or a fixed
15-minute wait deadline fails the candidate while the previous release keeps
serving. `gregale deploys status` and the dashboard stage summary show the
blocker. This gates release activation; candidate processes can boot while
waiting. Declarations allow at most 100 dependency conditions per workload.
Completion conditions, optional healthy dependencies, and healthy managed or job
targets are rejected.
See [ADR-646](adr/646-compose-dependency-release-gates.md).

Compose `healthcheck:` overrides are also retained for prebuilt image workloads:

```yaml
services:
  api:
    image: ghcr.io/team/api:production
    healthcheck:
      test: [CMD-SHELL, 'curl -f http://localhost:$${PORT}/ready || exit 1']
      interval: 15s
      timeout: 250ms
      start_period: 20s
      start_interval: 500ms
      retries: 3
```

String tests use `CMD-SHELL`; list tests accept `CMD`, `CMD-SHELL`, or `NONE`.
`disable: true` disables an inherited image check. Omitted or zero timing/retry
values and empty tests inherit image settings. Durations preserve exact timing;
positive values must be at least 1ms. `$$` preserves a dollar for the guest
command; scanning does not read the host environment to interpolate variables.
The check runs inside the image and requires its executable or shell to exist.
Accepted overrides, including the choice to inherit, survive app edits, retries,
and rollback. JSON scan plans expose them as `image_healthcheck`.

Newly assembled image deployments require a fresh successful command check
before serving readiness, alongside the existing network or worker readiness
and public-route verification. Startup grace, retries, and command timeouts
apply within the startup deadline. A failed or missing result blocks promotion;
boot, restore, and warm-pool resume cannot reuse a previous pass. `NONE` and
absent checks retain ordinary readiness. Previously assembled deployments need
reassembly or redeployment to gain this gate. Native boot/restore qualification
remains pending. Compose dependency release gates are described above. Compose
healthcheck overrides on source-built services are not applied and produce a
scan warning.
See [ADR-642](adr/642-compose-image-healthchecks.md) and
[ADR-643](adr/643-image-healthcheck-readiness.md).

Required primary image checks also drive runtime recovery. Each serving
instance executes fresh command attempts using the effective interval, timeout,
startup grace, and consecutive failure threshold. Repeated command failures
request scheduler-owned teardown and cold recovery, even if HTTP stays open.
The declared command replaces implicit HTTP liveness, so the image does not
need a /healthz endpoint. Explicit HTTP/gRPC liveness remains additional.
Successful checks clear the failure streak; process replacement and serving
resume start fresh monitoring. Missing or invalid transport proof requests
infrastructure recovery without consuming the app's permanent restart budget.
The existing restart circuit limits repeated confirmed failures. Failure
diagnostics use image_healthcheck_unhealthy without publishing command output.
Matching guest and vmmd support is required; older image rootfs releases need
reassembly. Native lifecycle qualification remains pending. See
[ADR-644](adr/644-image-healthcheck-runtime-recovery.md).

Automatic image promotion also checks for newer accepted releases in the same
app and environment scope. Older in-flight candidates become superseded, and
GitHub-triggered images recheck their recorded source branch before cutover.
Explicit rollback retains its existing behavior. See
[ADR-641](adr/641-image-deployment-promotion-ordering.md).

| Concern | Current contract | Qualification boundary |
|---|---|---|
| Image platform | Linux/amd64; compatible child selected from an OCI or Docker index | Other architectures and operating systems are rejected. |
| Process | Image ENTRYPOINT + CMD, environment, working directory, and user | Metadata inspection cannot prove executables or users exist in the layers. Numeric or named `USER user:group` is preserved; explicit missing identities fail launch. Supplementary memberships are not imported. |
| Main listener | Explicit deployment port, otherwise one exposed TCP port, otherwise 8080 | Multiple TCP declarations do not select the main port. Bind to `0.0.0.0`. |
| Additional listeners | Declared TCP listeners use `<slug>--port-<name>.<apps-domain>` selectors through the gateway | These are HTTP gateway selectors. Raw TCP uses separately configured app-owned listeners; UDP uses the separate opt-in listener service below. |
| Raw TCP ingress | Opt-in public edge binds stable app-owned TCP listener ports and forwards bytes through admission and vmmd; optional per-listener TLS termination | Disabled by default; requires gateway/firewall rollout. TLS defaults to passthrough; termination requires verified app-owned hostname and provisioned edge bundle. Native TLS acceptance remains pending. See [rollout](ops/tcp-ingress.md). |
| Private TCP between services | Same-account services dial `<name>.svc.gregale:<port>` on any declared TCP listener or the serving port; compose `expose:` declares internal-only listeners (ADR-576) | Node-level rollout; until enabled, service names answer HTTP only. Internal listeners never get public selectors or raw TCP listeners. Ports 10080, 10081 and 443 stay on the HTTP mesh. See [networking](networking.md#private-tcp-between-services). |
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
