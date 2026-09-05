# Inspect an OCI image before deployment

`gregale doctor --image REF` checks image metadata from the registry without
creating an app, starting a deployment, downloading filesystem layers, or
executing the container. No Gregale login is required.

```sh
gregale doctor --image registry.example.com/team/api:release
gregale doctor --image registry.example.com/team/api@sha256:<digest> --json --strict
```

The report includes the immutable image reference, OS/architecture, image
entrypoint and command, combined launch arguments, effective default user and
working directory, stop signal, declared ports, and health-check metadata.
Image environment values and registry credentials are omitted. These are image
defaults **before deployment overrides**, not the configuration of an existing app.

Checks cover:

- Registry metadata access from the machine running the CLI.
- Manifest/config content digests and the single-platform manifest contract.
- The production Linux/amd64 image target.
- The same image-to-runtime manifest validator used by deployment.
- The platform's known stateful-image name denylist. Passing this check does
  not prove an application is stateless.
- Health-check command shape, negative timing/retry values, stop-signal
  fallback, and image volume declarations that do not provision durable storage.

Many image tags identify a multi-platform index. Current Gregale deployment
requires a **platform-specific image manifest**: select the Linux/amd64 child
digest with your registry's image inspection tools, then pass that reference.
Pinning the index's own digest does not select a platform. Preflight follows
this deployment restriction rather than silently choosing a different image.

## Private registries

Supply a registry username and pipe a password or token through stdin:

```sh
your-secret-manager read registry-token | gregale doctor \
  --image registry.example.com/team/api:release \
  --registry-user build-user --registry-password-stdin --json
```

Credentials are transient and are not saved. Inspection uses the platform
registry client's Bearer-token authentication flow and egress policy; Docker
credential helpers, plain HTTP registries, and registries on private network
addresses are not supported by this command. Credentials saved for a Gregale
app are not downloaded to the CLI. A successful local inspection does not prove
that a deployment node has the necessary credentials or network access.

## Exit codes and limits

| Exit | Meaning |
| --- | --- |
| 0 | No errors; warnings are allowed unless `--strict` is set. |
| 1 | Inspection failed, an error was found, or `--strict` promoted a warning. |
| 2 | Invalid arguments or credential input. |

`--json` emits the structured doctor report. Image reports have an `image`
object and a `checks` array; source reports retain their `path` and `checks`.
Use each check's `status` (`ok`, `warn`, `error`, `skipped`) rather than assuming
every check was performed. Registry inspection has a bounded overall timeout.

Metadata cannot prove executable paths or named users exist in the filesystem,
the process starts, an application binds the right address/port, or a health
check actually succeeds. It also cannot verify account quotas, deployment
overrides, signatures, vulnerability/secret scans, base-layer compatibility,
or snapshot/resume behavior. Those checks remain the responsibility of
deployment validation and runtime testing. `EXPOSE` alone does not configure
Gregale's serving port or prove a listener exists.

The existing `gregale doctor [path]` source checks and `deploy --doctor-strict`
behavior are unchanged; image inspection is a separate, explicit preflight.
