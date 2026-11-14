# Source uploads preserve deployment configuration

Date: 2026-11-05
Status: Accepted
Amends: ADR-144 (workspace source root), ADR-20261026152731 (source deploy hardening)
Related: ADR-081 (workflow definitions), ADR-20261021152201 (scheduled releases)

## Context

Source uploads could read a configured nested workload's manifest while building
the repository root. Several transports also lost workflow declarations or an
explicit MCP task migration override. The SDK omitted a scheduled release time
when it was the request's only option, and an empty manifest caused HTTP 500.

## Decision

Resolve one source root before archive validation and manifest admission, then
persist that root for detection, Dockerfile selection and the build. Multipart
and resumable uploads use the app's configured root when `source_root` is omitted
or blank. An explicit `.` selects the archive root, and the SDK preserves that
selection on the wire. The CLI explicitly sends its selected root, using `.`
for archive-root builds so packed and cropped sources retain their behavior.
Legacy source-tarball uploads use the app's configured
root. Standalone apps with no configured root retain the archive-root default.
A configured root missing from an upload is rejected before enqueue.

Workflow declarations in the accepted archive supply the deployment's workflow
set when no nonempty set was supplied in the upload request. Existing explicit
multipart and resumable workflow declarations retain precedence. Source-ref
keeps its existing reviewed-declaration handling. All four transports pass the
validated workflow set through the shared rollout projection to enqueue.

Source-ref and source-tarball forward the validated, one-use MCP task migration
override to deployment creation. Compatibility checks remain active when the
override is absent, and successful cutover consumes the grant as before.

The SDK includes the source-tarball sidecar whenever its serialized request
contains options, including `release_at` and `release_notify`. Blank YAML or
TOML manifests have the same meaning as an absent manifest.

## Consequences

An existing app with a configured nested root now uses that root consistently
for local uploads. Clients uploading an already cropped archive for such an app
must send `source_root: "."`; legacy source-tarball clients must include the
configured directory or use multipart/resumable with the explicit root.

Regression tests cover all source transports, explicit root selection, nested
Dockerfiles, workflows, scheduling, empty manifests and task migration cutover.
No schema or runtime protocol change is required.
