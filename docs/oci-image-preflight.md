# Inspect an OCI image before deployment

`gregale doctor --image REF` checks image metadata from the registry without
creating an app, starting a deployment, downloading filesystem layers, or
executing the container. No Gregale login is required.

```sh
gregale doctor --image registry.example.com/team/api:release
gregale doctor --image registry.example.com/team/api@sha256:<digest> --json --strict
```

The report includes the requested reference, immutable source reference, selected
image reference, OS/architecture, image
entrypoint and command, combined launch arguments, effective default user and
working directory, stop signal, declared ports, and health-check metadata.
Image environment values and registry credentials are omitted. These are image
defaults **before deployment overrides**, not the configuration of an existing app.

Checks cover:

- Registry metadata access from the machine running the CLI.
- Manifest/config content digests and compatible platform selection.
- The production Linux/amd64 image target.
- The same image-to-runtime manifest validator used by deployment.
- The platform's known stateful-image name denylist. Passing this check does
  not prove an application is stateless.
- Health-check command shape, negative timing/retry values, stop-signal
  fallback, and image volume declarations that do not provision durable storage.

Many image tags identify a multi-platform index. Gregale automatically selects
its **Linux/amd64 image** in both preflight and deployment, including on an ARM
laptop running the CLI. OCI indexes and Docker manifest lists are supported.
You can pass a tag, an index digest, or a platform-specific image digest:

```sh
gregale doctor --image busybox:1.36 --json
```

The source reference identifies the immutable index (or original single image).
The selected image reference identifies the child used for config and layer
reads. Deployment verifies required signatures against the immutable source,
then builds from the selected child. This does not add ARM execution or emulation.

Selection accepts baseline amd64 (no variant or `v1`) and skips ARM, Windows,
and unknown-platform attestation entries. Missing or multiple distinct compatible
images fail with a platform diagnostic. Publish one compatible image or pin the
intended child digest. Nested indexes and additional CPU/OS requirements are not
supported. Existing deployment checks, including Gregale base-layer compatibility,
still apply; selecting a platform does not make every Docker image deployable.

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
