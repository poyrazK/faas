# ADR-20261028100000: PR preview values, build share and lifetime

- **Status:** accepted
- **Date:** 2026-10-28
- **Amends:** ADR-310 (preview quotas), ADR-1136 and ADR-1139 (inherited project values), ADR-20261026090000 (PR previews GA)
- **Decision:**
  1. **Previews get the values their owner chose for them.**
     - A production app's `preview` scope holds the variables and sealed secrets its PR previews receive. The owner sets them with the existing commands, for example `gregale env set --app web --scope preview DATABASE_URL=...`.
     - At wake, schedd delivers them to every PR preview of that app as inherited values. This is the slot a production deployment uses for its project environment (ADR-1136, ADR-1139), and PR previews still inherit no project environment.
     - The preview's own values win over inherited ones, as project values already work. Production's other scopes still never reach a preview (ADR-310).
     - Managed Postgres and object-storage credentials and ephemeral secrets stay with the app that owns them.
     - A secret is sealed to its account and key, not its app, so the preview's VM opens it like an inherited project secret.
     - The runtime configuration fence covers inherited values. A preview snapshot taken before a change does not restore; the next start boots with the new values.
     - A service that exists only in the pull request has no production app, so it gets no preview values.
  2. **PR preview builds use a share of the hourly deploy budget.**
     - A GitHub PR preview build is admitted only while the account's hourly deploy count is below `PreviewDeploysPerHour`, half of `DeploysPerHour` on every plan. Production deploys keep the rest.
     - Builders claim every other queued build before a PR preview build. This applies to the polling claim, and a notification never claims a preview build while another build is queued.
  3. **A preview goes with its production app.**
     - The preview janitor marks a PR preview for teardown when the production app behind its environment's root is gone. A production app in its delete grace counts as gone. The janitor then tears the preview down on the same pass.
     - The same rule removes previews left behind by a deleted app before this change.
  4. **Previews stay out of search engines.** The gateway answers every request for a preview app with `X-Robots-Tag: noindex, nofollow` and serves it a disallow-all `robots.txt`, whatever its production app serves.
  5. **API previews match webhook previews.**
     - `POST /v1/apps/{slug}/previews` no longer copies the production app's minimum instances, so a preview scales to zero.
     - Preview slugs may be up to 63 characters (one DNS label), as githubd already allocates them. apid's slug checks accept them.
- **Why:** A second GA review found these gaps:
  - A preview had no way to receive credentials, so any app that needs a database URL or an API key could not run in one.
  - Pushes to one pull request could spend the account's whole hourly deploy budget and queue ahead of a production deploy on the builder.
  - Deleting a production app left its previews serving PR code, and holding the preview allowance, for up to the lease length.
  - Preview hosts were indexable.
  - API previews of an app with warm instances kept them warm, and `gregale preview create` refused slugs longer than 40 characters.
- **Consequences:**
  - Writing a value in the `preview` scope is an ordinary scoped secret write, so it invalidates the production app's snapshots once, like any secret change.
  - A running preview keeps its values until it next starts. Changes reach running previews on their next wake, not through the runtime reload path.
  - `PreviewDeploysPerHour` is a new limit in `pkg/api/limits.go` (Free 5, Hobby 25, Pro 125, Scale 500). It needs no migration, because previews and production share the existing hourly counter.
  - A preview build can wait behind a long queue of production builds. Production deploys are already bounded by the hourly budget.
  - No migration: the runtime snapshot queries read the parent's `preview` scope, and orphan detection reads existing columns.
- **Rejected alternatives:**
  - **Copy production values into previews.** ADR-310 rules it out: preview code is unreviewed.
  - **Use a project environment named `preview`.** Project environments are a separate preview-tier capability that is off for most accounts. A legacy single-app preview has no project.
  - **Copy secrets onto the preview app at reservation.** Copies go stale when the owner changes a value, and they would add apid writes to githubd's reservation path.
  - **A separate preview deploy counter.** It needs a migration, and it would not keep production's share free.
