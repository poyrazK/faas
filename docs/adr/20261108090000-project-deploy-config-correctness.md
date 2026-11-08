# ADR-20261108090000: Project deploys scope bundled secrets, explain environment gaps and caller lists, and report reviewed-only settings on pushes

- Status: accepted
- Date: 2026-11-08
- Amends: ADR-20261105095300 (Compose app protocol), ADR-20261103112845
  (GitHub pushes and reviewed-only changes), the project secrets bundle

## Context

- **Protocol on stage deploys.** A deploy to a named environment never
  applied `x-gregale-app-protocol`, so a gRPC service first created there
  stayed `http1`.
- **Missing stage dependencies.** A stage deploy that left out a dependency
  with no deployment in that environment succeeded. Every call to it was then
  refused with 403, and the plan said nothing.
- **Caller lists.** `x-gregale-allow-callers` names were never checked. A
  typo, or a caller renamed with `x-gregale-renamed-from`, refused that
  caller's every call silently.
- **Secrets bundle.** `--secrets-file` wrote every key to every selected
  service: one service's credentials reached every other service. It also
  replaced values a service had set itself, rewrote unchanged values (each
  write evicts the app's snapshots), and could stop partway on a quota error.
- **Root gregale.yaml on pushes.** A GitHub push never applies the project's
  root `gregale.yaml` routes, async routes or database bindings, and nothing
  on GitHub said so.

## Decision

1. A named-environment deploy sets the declared protocol on the apps it
   creates. Existing apps are left alone, because the protocol is app-wide
   and a stage deploy must not change production.
2. The plan of a named-environment deploy warns, for each selected service,
   about each project dependency that is not selected and has no live
   deployment in that environment.
3. The plan warns about each `x-gregale-allow-callers` name that is no
   scanned service, no project app and no account app slug.
4. A secrets-bundle key goes to the selected services whose Compose
   environment names it. A key no service names goes to every selected
   service without a value for it, never replacing one. Values whose
   fingerprint matches the stored one are skipped. Every app's secret limit
   is checked before the first write.
5. A push whose changed files include the root `gregale.yaml`, when that file
   declares routes, async routes or databases, writes a failed
   `gregale / <project> (project settings)` check. The check names them and
   says to run `gregale deploy`. The push's services still deploy. Applying
   these settings stays a reviewed action, like removing a service.

## Consequences

- A bundle key a service reads without declaring it in Compose no longer
  reaches that service when another service declares it. Such a service must
  declare the key or have it set directly.
- The settings check fires on any push that touches the root `gregale.yaml`
  while it declares those sections, even when the push did not change them;
  the summary says what a reviewed deploy applies.
- A push compared without a file list (the full-fan-out fallback) writes no
  settings check.

## Alternatives rejected

- Applying root routes and bindings on pushes: this needs apid to accept a
  project manifest from githubd, and puts unreviewed route changes into
  production.
- Failing the stage deploy instead of warning: a stage may intentionally run
  only part of a project.
