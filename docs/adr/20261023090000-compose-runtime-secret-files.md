# Compose service secrets as files in /run/secrets

Date: 2026-10-23
Status: Accepted
Amends: ADR-1337 (Compose key reporting)
Related: ADR-20261022185329 (Compose build secrets), ADR-222 and ADR-503 (live secret reload), ADR-20261022111812 (seal domains), spec §11 / gap G2 (secrets at boot)

## Context

A Compose service reads a secret from a file: `secrets: [db_password]` mounts
`/run/secrets/db_password`, and images such as `postgres` and `mysql` read
`*_FILE` variables pointing there. The scan warned that service-level
`secrets` were not mounted, so those services started without the file and
failed, or needed their image changed to read an environment variable.

Gregale already delivers an app's secrets to its VM: vmmd opens them with the
`runtime` identity and writes `/etc/faas/secrets.env` into the instance drive,
and guest-init passes them to the workload's environment, refreshing them live
when the image opts in (ADR-222). What was missing is putting a value in a
file.

## Decision

A Compose service's `secrets` become files in `/run/secrets` of its main
workload, written by guest-init from the secrets it already receives.

- **Declaration.** `secrets` lists secret names, or `{source, target, mode}`.
  Each names a top-level `secrets:` entry declared with `environment: NAME`,
  and `NAME` is the secret the app receives as the environment variable
  `NAME` (`gregale secrets set NAME`), as for build secrets
  (ADR-20261022185329). A `file:` or external source is not uploaded: the scan
  warns and leaves it out. An undeclared name fails the scan.
- **Where.** `target` is a file name in `/run/secrets` and defaults to
  `source`; an absolute target in `/run/secrets` is the same. A target
  anywhere else is warned and left out rather than written into the image's
  filesystem. Names are letters, digits, `.`, `_` and `-`, start with a letter
  or digit, and are unique. `pkg/api/limits.go` caps the count
  (`SecretFilesMax`).
- **Mode and owner.** `mode` defaults to Compose's `0444` and may be any
  permission bits (`0440`, or the string `"0400"`). The file is owned by the
  user the workload runs as, so a restrictive mode still lets it read the
  file. `uid` and `gid` are warned and ignored.
- **Stored as names.** The scan's `SecretFile{Secret, Name, Mode}` list is
  project-managed on the app (`project_secret_files` in the manifest JSONB, no
  migration) and changing it updates the app. imaged copies it into the guest
  manifest (`secret_files`, `omitempty`) for built and prebuilt images, from
  the app as it does generated service URLs, so each deployment's layer keeps
  the list it was built with. No value is stored, logged or shown in plans.
- **Written in memory.** Before the workload starts, guest-init mounts a
  tmpfs (`nosuid,nodev,noexec`, sized from `SecretFilesMax` and the largest
  plan's `SecretValueMaxBytes`) at `/run/secrets` and writes each file
  atomically. The files stay in guest memory, as the environment does, and
  are never written to the instance drive. A secret with no value has no file,
  and guest-init logs its name. A mount or write failure stops the boot.
- **Live reload.** When the image opts into live secret reload (ADR-222), each
  new generation rewrites the files before the projection is published and
  the process signalled; a deleted secret's file is removed. A failed write
  fails that publication, so the reloader retries both. Without reload, the
  files change on the next start, as the environment does.
- **Scope.** Files are written for the main workload only. Sidecars, jobs,
  app tasks and builds do not receive them.

## Consequences

- `*_FILE` conventions and images that read `/run/secrets` work unmodified.
- A secret written to a file is also in the app's environment, because both
  come from the same secrets. Compose sets no variable for it.
- Restored instances keep the files their snapshot captured, as they keep
  their environment; reload-enabled images refresh both.
- Release skew: an imaged or guest-init from before this change ignores the
  list, so the files appear once both run this version (runtime units deploy
  last); a redeploy picks them up.
- Native acceptance (an image reading `/run/secrets` at boot and after a
  reload) is pending with the other container lanes.
