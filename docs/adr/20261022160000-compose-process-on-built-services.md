# Compose process overrides on source-built services

Date: 2026-10-22
Status: Accepted
Amends: ADR-1337 (Compose process overrides applied to prebuilt images only)
Related: ADR-20261019195000 (Compose healthchecks on built services), ADR-20261019142544 (build arguments and target)

## Context

ADR-1337 applies a prebuilt image's Compose `entrypoint`, `user`,
`working_dir`, `stop_signal` and `stop_grace_period`, and warns that the same
keys on a `build:` service are ignored because "the Dockerfile owns them".
Compose applies them to built images too: they are run-time overrides of the
image, whoever built it. A service that builds its image and sets `user: node`
or `stop_signal: SIGINT` ran as the Dockerfile's user and stopped with
SIGTERM, and the only signal was a warning.

## Decision

A built service takes the same overrides.

- **entrypoint.** A built service's `command` already becomes its start
  command, which replaces the image's process. The scan therefore folds an
  entrypoint into that command, giving Docker's argv: the entrypoint alone
  when there is no command (an entrypoint override drops the image CMD), the
  entrypoint followed by the command, or the command alone for
  `entrypoint: []`. A shell-form command is appended as shell words after the
  quoted entrypoint. The validation rules are ADR-1337's: list or plain-word
  string form, and an empty entrypoint needs a command.
- **user, working_dir, stop_signal, stop_grace_period.** The scan validates
  them as for a prebuilt image, reconcile stores them as the app's
  `project_image_process` (a change is an `image_process` app update), and
  project admission applies the plan's stop-grace cap. Enqueue freezes them in
  the deployment's inferred profile (`build_process`) next to the Dockerfile
  path, build options and Compose healthcheck; stage retries copy the profile.
  imaged applies them to the built image's config before deriving the guest
  manifest, exactly where a Dockerfile's USER, WORKDIR and STOPSIGNAL land, so
  a named user is resolved the same way.

## Consequences

- Built Compose services run as the user, in the directory and with the stop
  contract their file declares, and the scan stops warning about these keys.
- A redeploy of a service that declared them under the ADR-1337 warning
  changes its process; the plan shows the change before it is applied.
- An older imaged ignores `build_process` and builds with the image's own
  settings, which is today's behavior; apid and imaged ship in the same
  control-plane step.
- Native coverage is pending with the other container lanes.
