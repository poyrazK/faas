# Each Compose include interpolates with its own .env

Date: 2026-10-25
Status: Accepted
Amends: ADR-20261019190527 (Compose extends and include), ADR-20261022190000 (Compose profiles from .env)

## Context

ADR-20261019190527 resolves top-level `include`, but interpolates every
service with the root `.env`. An include's `env_file` option and a `.env`
beside an included file were warned and ignored. A project that splits its
services into directories, each with its own `.env` (a common layout for
`include`), therefore got the root's values or empty substitutions, unlike
under Compose.

Separately, `ReadComposeEnv`, which feeds environment import, still left out
every service with `profiles:` after ADR-20261022190000 let the scan deploy
services whose profile `COMPOSE_PROFILES` activates. Import and deploy
disagreed about which services exist.

## Decision

An included Compose file is interpolated with its own project's values, as
Compose builds them.

- **Values.** An included project starts from the including project's values
  (the root `.env` for a top-level include) and overlays, in order, the files
  the entry's `env_file` names (a string or a list), or else the `.env` beside
  the included file when there is one. A nested include extends its parent's
  values the same way. Root services keep the root `.env`.
- **Paths.** `env_file` paths resolve from the including file's directory, like
  the include's `path`, and must stay inside the source. A named env file that
  does not exist fails the scan, as in Compose; a missing default `.env` is
  simply absent.
- **Where the values apply.** Everything the scan interpolates per service uses
  them: the service's own fields and its `build.args` defaults.
  `COMPOSE_PROFILES` and the top-level `x-gregale-*` keys remain the root
  project's.
- **Not applied.** `project_directory` still warns. Paths and the default
  `.env` resolve from the included file's directory, which is Compose's
  default for `project_directory`.
- **Environment import.** `ReadComposeEnv` reads the same linked project and
  keeps a service with profiles when `COMPOSE_PROFILES` in the root `.env`
  activates one, as the scan does.

## Consequences

- Projects that keep a `.env` per included directory deploy with the values
  Compose would use, and the "not read" warnings are gone.
- A project whose include names an `env_file` that is not committed now fails
  the scan with the file's name instead of deploying with root values.
- Services behind an active profile are offered for environment import.
