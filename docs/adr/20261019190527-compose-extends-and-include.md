# Resolve Compose extends and include

Date: 2026-10-19
Status: Accepted
Amends: ADR-1337 (`extends` and top-level `include` were warned and not resolved)
Amended by: ADR-20261025090000 (each include interpolates with its own .env)
Related: ADR-1288 (Compose build context), ADR-20261019142544 (Compose build arguments and target)

## Context

ADR-1337 made the scan warn about `extends` and top-level `include` instead
of dropping them silently, and left resolving them for later. Both are common
in real projects. A service that extends a base deployed without the base's
image, environment or ports. A file that includes others deployed without the
included services. The warning named what was missing, but the deployment was
still not what the file describes.

## Decision

The scan resolves both before it reads services, as Compose does.

- **extends.** `extends: base` and `{service: base}` refer to the same file;
  `{service, file}` loads another file, relative to the declaring file and
  inside the source. The base is resolved first (chains work) and the service
  is merged onto it with the rules Gregale already uses for override files:
  mappings merge with the service winning, `ports`, `env_file`, `profiles`,
  `depends_on` and `environment` lists append without duplicates, and other
  values are replaced. Short forms are expanded first (`build: dir`, an
  `environment` list), so they merge rather than replace. A cycle, an unknown
  base or a missing file fails the scan, as in Compose.
- **include.** Each entry, short or long form, loads one or more files (a
  `path` list is merged in order, like `-f a -f b`) together with their own
  `include` and `extends`. Their services join the project. A service name
  defined in two files fails the scan, as in Compose; so does a cycle or a
  missing file. A remote include (`https://`, `git@`, `oci://`) is not loaded
  and warns.
- **Paths.** A service loaded from another directory has its build context
  and `env_file` paths rebased to the repository root, so `build: .` in
  `services/api/compose.yaml` builds `services/api`, as in Compose.
- **Variables.** Interpolation still reads the root `.env`. An include's
  `project_directory` and `env_file` options are not applied, and a `.env`
  next to an included file is not read; each case warns.
- **Bound.** At most 32 files are linked through include and extends in one
  scan; more fails the scan rather than fanning out without limit.
- **Unchanged files.** A project without `include` or `extends` is decoded
  exactly as before.

## Consequences

- Services that extend a base, and included services, deploy as Compose
  describes them. A project that deployed with the ADR-1337 warning gets the
  inherited settings on its next plan, and the plan shows them as changes
  before they are applied.
- A file with a dangling `extends`, a missing include, or a service defined
  twice now fails the scan with an error naming the files, where it used to
  deploy with a warning. Compose rejects these files too.
- Native acceptance of an included multi-directory project is pending with
  the other container lanes.
