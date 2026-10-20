# Compose build arguments and target stage for Dockerfile builds

Date: 2026-10-19
Status: Accepted
Amends: ADR-1337 (Compose key reporting; `build.args` and `build.target` were warned and deferred)
Related: ADR-1665 (public build URLs as Dockerfile build arguments), ADR-003 (builder microVMs)

## Context

ADR-1337 made the scan report every Compose key it ignores. `build.args` and
`build.target` were among them, deferred because carrying them "would need
builder, cache-key and frozen-profile changes, and build argument values would
become stored configuration". They are the two build keys real Compose files
use most: a multi-stage Dockerfile whose last stage is a development or test
image needs `target: production`, and `args` select versions and feature
switches. Today such a service builds the wrong stage, or with every `ARG` at
its default, and the only signal is a scan warning.

ADR-1665 already passes reviewed public URLs to Dockerfile builds as BuildKit
build arguments, fences older builder guests with an acknowledged digest, and
keys the artifact cache on the values. This record reuses that path.

## Decision

A source-built Compose service passes `build.args` and `build.target` to its
Dockerfile build, as `docker compose build` does.

- **Shape.** `api.DockerfileBuildOptions` holds `args` (name to value) and
  `target`. The scan puts it on the workload, reconcile stores it on the app as
  `dockerfile_build` (project-managed, like `build_dockerfile`), and a change
  to it is an app update reported as `dockerfile_build`. The plan shows the
  target and the argument names, not their values.
- **Values.** `args` accepts the map and list forms. Values are interpolated
  like the rest of `build:` (`${VAR}` from the source's `.env`). A name without
  a value (`- VERSION`, or `VERSION:` with no value) reads the source's `.env`,
  as Compose reads its project environment; if `.env` does not set it, the
  argument is left out, the Dockerfile's `ARG` default applies, and the scan
  warns. A value that still contains `$NAME` without braces warns, because
  only `${NAME}` is interpolated. Numbers and booleans are written as Compose
  writes them.
- **Validation.** Argument names follow the environment-variable grammar.
  `pkg/api/limits.go` caps the number of arguments, each value and the encoded
  total; values cannot contain NUL. The target must be a valid stage name,
  and the scan fails if the Dockerfile declares no `FROM … AS <target>`, so a
  typo fails the plan instead of the build. A service whose build context has
  no Dockerfile builds with Railpack; its `args` and `target` are warned and
  not stored.
- **Not for secrets.** Build arguments are visible in the image history, as
  in Docker. `build.secrets` and `build.ssh` stay unsupported and warned;
  runtime secrets belong in `gregale secrets`.
- **Per deployment.** Enqueue copies the app's options into the deployment's
  inferred profile, next to its Dockerfile path, so a queued build uses the
  options its plan was applied with. builderd reads the profile first and the
  app manifest for deployments without one, the same precedence as
  `build_dockerfile`. A reviewed environment source selection (GitOps) replaces
  the app's build settings, so it clears them; inheriting a live source into a
  scoped environment refuses an app with build options, as it refuses other
  conflicting build metadata.
- **Builder.** builderd sends the options only to Dockerfile builds and logs
  that they were ignored otherwise. Guest-init adds `--opt target=` and one
  `--opt build-arg:` per name, sorted. A public build URL (ADR-1665) with the
  same name wins, because it is reviewed configuration. The options are part of
  the artifact cache recipe, so changed arguments or target never reuse an
  artifact, and recipes without them keep their keys.
- **Old guests fail closed.** A successful guest acknowledges the options'
  digest in `BuildDone`; builderd fails a build whose acknowledgement is
  missing or different, as for public build URLs. An older builder image would
  otherwise build the final stage with default arguments.

## Consequences

- Multi-stage Compose builds produce the stage the file names, with its
  arguments, and the scan stops warning about `build.args` and `build.target`.
- The argument values are stored on the app and the deployment, and are
  readable by anyone who can read the source. They never appear in plan
  responses, API projections of the profile, or platform logs; a Dockerfile
  that echoes an argument prints it in the customer's own build log, as
  `docker build` does.
- No migration: the app manifest and the inferred profile are JSON columns,
  and every new key is omitted when empty, so existing rows, recipes and
  builder manifests are byte-identical.
- apid and builderd release together in the control-plane step; a newer apid
  beside an older builderd stores options the older builderd ignores, and the
  build uses the final stage until builderd is upgraded. The guest fence covers
  the runtime step, which ships last.
- Native builder acceptance (a multi-stage Dockerfile built with and without a
  target, and a changed argument on identical source) is pending with the other
  native lanes.
