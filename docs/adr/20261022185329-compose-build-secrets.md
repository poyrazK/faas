# Compose build secrets for Dockerfile builds

Date: 2026-10-22
Status: Accepted
Amends: ADR-20261019142544 (Dockerfile build options), ADR-1337 (Compose key reporting)
Related: ADR-20261022111812 (seal domains), ADR-003 (builder microVMs), ADR-423 (sealed env)

## Context

A Dockerfile that installs private packages needs a token at build time:
`RUN --mount=type=secret,id=npm,env=NPM_TOKEN npm ci`. Compose passes it with
`build.secrets`, naming a top-level secret whose value comes from the
environment. The scan warned that build secrets were not available, so those
builds failed or fell back to a token baked into the image.

The value must reach the builder microVM without widening who can decrypt
customer secrets. ADR-20261022111812 seals `app_secrets` to the `runtime`
domain, whose only reader is vmmd. builderd reads no domain and must not
start to.

## Decision

A Dockerfile build receives the build secrets its Compose service declares,
taken from the app's secrets.

- **Declaration.** `build.secrets` lists secret names (or `{source, target}`,
  where `target` is the id the Dockerfile mounts, defaulting to `source`). Each
  names a top-level `secrets:` entry declared with `environment: NAME`, and
  `NAME` is the app secret that holds the value (`gregale secrets set NAME`).
  A `file:` or external source is not uploaded: the scan warns and leaves that
  secret out. An undeclared name fails the scan. Ids and names are validated,
  ids are unique, and `pkg/api/limits.go` caps the count
  (`DockerfileBuildSecretsMax`).
- **Stored as names.** The id-to-name pairs join `api.DockerfileBuildOptions`
  (ADR-20261019142544): stored on the app, frozen per deployment, part of the
  cache recipe and of the guest acknowledgement. No value is stored, logged,
  shown in plans or keyed into a cache; a rotated token does not invalidate
  a cached artifact, as in Docker.
- **Opened only by vmmd.** builderd reads the named rows' ciphertext from
  `app_secrets` in the deployment's scope, as schedd does for a wake, and
  passes them in the builder's `AppSpec.sealed_env`. vmmd opens them with the
  `runtime` identity it already holds and writes `/etc/faas/secrets.env` into
  the builder's own drive, the path an app VM's secrets take. builderd joins
  schedd as a caller that hands runtime ciphertext to vmmd; it gains no key. A
  missing secret fails the build with the names, not the values. Project
  environment secrets and secret aliases are not resolved for builds in this
  version.
- **Used once.** Guest-init reads the file, deletes it, and gives each value
  to buildctl through `--secret id=<id>,env=<variable>` in buildctl's own
  environment. BuildKit secret mounts are not written to layers, the image
  history or its cache. A Railpack build (no Dockerfile) ignores build secrets
  with a scan warning. A guest that does not deliver a declared secret fails
  the build, and older guests fail closed on the acknowledgement.
- **No warm builder.** A build with secrets never restores or leaves a warm
  builder snapshot, so a value cannot outlive its build in a snapshot's memory
  or drive. The export copies only `build-done.json` and `/build/out/*`, and
  builderd deletes the drive afterwards.

## Consequences

- Private dependencies install at build time from `gregale secrets` without
  being baked into images.
- A Dockerfile that prints a secret prints it in the customer's own build log,
  as `docker build` does.
- Builds with secrets are cold builds.
- Native acceptance (a private npm install through a mounted secret) is
  pending with the other container lanes.
