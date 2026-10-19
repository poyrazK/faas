# ADR-1337: Compose process overrides and unsupported-key reporting

- **Status:** accepted
- **Date:** 2026-10-18
- **Decision:** Prebuilt image services honour Compose `entrypoint`, `user`,
  `working_dir`, `stop_signal` and `stop_grace_period`, frozen per deployment
  beside the Compose command (ADR-686). `env_file` contributes variable names,
  like `environment:`. Every other Compose key is reported as a scan warning
  instead of being dropped silently, including `extends` and top-level
  `include`, whose warnings name exactly what was not loaded.
- **Why:** The scan decoded only the keys it used and ignored the rest without
  a signal. A service declaring `user: 1000` or `entrypoint: [tini, --]` ran
  the image's own process; one declaring `volumes:` lost its data on the next
  start; an `env_file` declared required variables nobody was told to set.

## Process overrides (prebuilt images)

- **Shape:** `api.ComposeProcess` holds `entrypoint` (a non-nil empty list
  clears the image ENTRYPOINT), `user`, `working_dir`, `stop_signal` and
  `stop_grace_period_s`. The scan exposes it as `image_process` on plan
  workloads, reconcile stores it as the app's `project_image_process`, and a
  change to it is an app update like `image_healthcheck`.
- **Semantics:** As with `docker run --entrypoint` and Compose, an entrypoint
  override drops the image CMD; the Compose `command`, if any, supplies the
  arguments. An empty entrypoint therefore requires a command. String
  entrypoints are split on spaces only; quoting or shell syntax requires the
  list form, because splitting it would need a shell.
- **Validation:** `user` follows the OCI user/group grammar and is resolved
  against the image at assembly (ADR-1239). `working_dir` must be a clean
  absolute path. `stop_signal` must be one guest-init delivers (SIGTERM,
  SIGINT, SIGQUIT, SIGHUP, SIGUSR1, SIGUSR2); any other would silently become
  SIGTERM. `stop_grace_period` is whole seconds, at least 1s, at most the
  absolute cap, and project admission applies the plan's cap (ADR-138).
- **Frozen contract:** The deployment's `image_command` record (ADR-686)
  gains a `process` field and version `v2`. Records without process overrides
  stay `v1`, so existing and unaffected deployments are byte-identical. An
  imaged that predates `v2` rejects the record and fails the deployment
  instead of starting the image's own entrypoint, user or directory. A `v1`
  record carrying a process, or a `v2` record without one, is rejected.
- **Materialization:** imaged applies the frozen command and process to the
  OCI config before deriving the guest manifest, so ENTRYPOINT/CMD, USER,
  WORKDIR, STOPSIGNAL and the stop grace period reach the guest through the
  existing manifest fields. Explicit deployment overrides keep their
  precedence (ADR-053).
- **Source builds:** These keys on a `build:` service are not applied: the
  Dockerfile owns ENTRYPOINT, USER, WORKDIR and STOPSIGNAL there. The scan
  warns, as it already does for healthchecks (ADR-682).

## env_file

`env_file` accepts a path, a list of paths, and `{path, required, format}`
entries (`format: raw` only). A path outside the source is not read and warns. The scan
reads the dotenv grammar Compose accepts: `KEY=value`, `KEY=` with an empty
value, `KEY` alone, `export` prefixes, blank lines, `#` comments, an inline
`# comment` after an unquoted value, and single- or double-quoted values with
spaces and backslash-escaped quotes that may span lines. Quoted values are
consumed to their closing quote, so a multi-line value (a PEM key, say) never
yields bogus keys. It keeps only the names and merges them with
`environment:` keys. Values are never read into the plan: they are set
through `gregale env` or secrets. A required file that is absent from the
source, commonly a git-ignored `.env`, produces a warning; `required: false`
silences it. A line it cannot read, or an unterminated quote, produces a
warning naming the file and line, and that line is skipped: such files were
ignored before this ADR, so failing the scan would break projects that
deploy today.

## Key reporting

The decoder stays non-strict. The scan decodes each service loosely and
classifies its keys:

- **Applied:** the keys above plus `build` (`context`, `dockerfile`),
  `command`, `depends_on`, `expose`, `healthcheck`, `image`, `ports`,
  `profiles` and `x-gregale-*`.
- **Quietly ignored:** `container_name`, `init`, `stdin_open`, `tty`,
  `platform: linux/amd64` and `x-*` extensions, which mean nothing for a
  microVM service.
- **Warned:** volumes and tmpfs, resource and replica keys, `restart`,
  `secrets` and `configs`, networking keys, kernel and device controls, guest
  DNS and host keys, `labels`, `logging`, `pull_policy` and other platforms,
  each with the platform's alternative; unknown keys get a generic warning.
  `build.*` keys other than `context` and `dockerfile` (including `args`,
  `target` and `secrets`) are warned: the builder builds the Dockerfile's final
  stage without build arguments.
- **Not resolved, with a prominent warning:** `extends` and top-level
  `include`. The scan does not merge the extended service or the included
  files. The `extends` warning names the base service and, for a base in the
  same file, the inherited keys that were not loaded (or the other file it
  names); the `include` warning lists the files whose services were not
  loaded. Both tell the user to inline them. Rejecting them would break
  projects that deploy today, when these keys were already ignored.

Managed database and cache services are not reported: the managed resource
replaces the container and its keys.

## Consequences

- A project using `extends` or `include` keeps deploying as before, now with a
  warning that names the settings or services it does not get.
- Build arguments and targets remain unsupported for source builds. Carrying
  them would need builder, cache-key and frozen-profile changes, and build
  argument values would become stored configuration; that is a separate
  decision.
- Published ports keep their existing meaning. For prebuilt images the
  container target selects the main port (ADR-678); for source builds the
  published port only labels the plan, since the build's serving port comes
  from its runtime profile.
- Roll out imaged before apid: an older imaged fails `v2` deployments closed
  rather than running them with the image's defaults.
