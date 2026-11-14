# ADR-20261108140000: PR previews refuse a missing datastore value and a deploy share that cannot build the whole set

- Status: accepted
- Date: 2026-11-08
- Amends: ADR-20261028090000 (preview configuration), ADR-20261028100000
  (PR preview build share)

## Context

- **Datastores.** A PR preview gets no database or other datastore of its
  own, and never its production app's managed credentials. A Compose value
  such as `DATABASE_URL: postgres://app:secret@db:5432/app` is a Docker
  address, so it is not imported either. A preview service that reads such
  an address started without it, failed readiness, and the PR's check gave a
  generic failure. Production pushes already hold such services and name the
  variable (ADR-20261103112845); previews did not.
- **Deploy share.** Each preview build takes one deploy from the PR preview
  share of the hourly deploy limit, admitted build by build. When the share
  ran out part-way, the services queued first built the new head and the
  rest stayed on the old one. The set failed, and the deploys already spent
  were kept. ADR-20261105095300 fixed the same problem for pushes.

## Decision

1. Before reserving the preview set, githubd checks each previewed service
   that reads a Compose datastore's address. If neither the PR's Compose
   import nor the production app's `pr-preview` scope gives it a value, the
   preview setup fails. The check names each service, variable and datastore,
   and the `gregale env set --app <app> --env pr-preview` command to run.
   Nothing is reserved or built.
2. Before reserving the set, githubd reads the account's remaining PR preview
   deploys. When they are fewer than the set's workloads, the preview setup
   fails with the need, the remainder and the window reset. Nothing is
   reserved or built. As for pushes, the read is advisory: the bridge's
   per-build admission still gates each build.

## Consequences

- A preview that would have started broken now fails before building and
  spends no deploys.
- A preview set that does not fit stays on its previous commit until the
  window resets.

## Alternatives rejected

- Rebuilding only the preview services whose files changed. A preview set is
  live only when every member has a live deployment at the recorded head
  commit (`previewset.Evaluate`), and every push refreshes
  `GREGALE_GIT_COMMIT`. Skipping a member would need per-member head commits
  in the preview set, its status and its check. That is a separate change.
  Pushes that change only ignored paths are already skipped.
- Holding only the affected service. A preview runs the PR's dependency
  closure together, so a service without its datastore leaves its callers
  broken too.
