# ADR-20261028100000: PR preview build share, lifetime and indexing

- **Status:** accepted
- **Date:** 2026-10-28
- **Amends:** ADR-310 (preview quotas), ADR-20261026090000 (PR previews GA)
- **Decision:** Preview configuration is ADR-20261028090000. This ADR fixes the other gaps a second GA review found.
  1. **PR preview builds use a share of the hourly deploy budget.**
     - A GitHub PR preview build is admitted only while the account's hourly deploy count is below `PreviewDeploysPerHour`, half of `DeploysPerHour` on every plan. Production deploys keep the rest.
     - Builders claim every other queued build before a PR preview build. This applies to the polling claim, and a notification never claims a preview build while another build is queued.
  2. **A preview goes with its production app.**
     - The preview janitor marks a PR preview for teardown when the production app behind its environment's root is gone. A production app in its delete grace counts as gone. The janitor then tears the preview down on the same pass.
     - The same rule removes previews left behind by a deleted app before this change.
     - A service that exists only in the pull request has no production app; it stays as long as its root's production app exists.
  3. **Previews stay out of search engines.** The gateway answers every request for a preview app with `X-Robots-Tag: noindex, nofollow` and serves it a disallow-all `robots.txt`, whatever its production app serves.
  4. **API previews match webhook previews.**
     - `POST /v1/apps/{slug}/previews` no longer copies the production app's minimum instances, so a preview scales to zero.
     - Preview slugs may be up to 63 characters (one DNS label), as githubd already allocates them. apid's slug checks accept them.
- **Why:** The second review found these gaps:
  - Pushes to one pull request could spend the account's whole hourly deploy budget and queue ahead of a production deploy on the builder.
  - Deleting a production app left its previews serving PR code, and holding the preview allowance, for up to the lease length.
  - Preview hosts were indexable.
  - API previews of an app with warm instances kept them warm, and `gregale preview create` and the dashboard refused slugs longer than 40 characters.
- **Consequences:**
  - `PreviewDeploysPerHour` is a new limit in `pkg/api/limits.go` (Free 5, Hobby 25, Pro 125, Scale 500). It needs no migration, because previews and production share the existing hourly counter.
  - A preview build can wait behind a long queue of production builds. Production deploys are already bounded by the hourly budget.
  - No migration: orphan detection reads existing columns and `pr_preview_sets`.
- **Rejected alternatives:**
  - **A separate preview deploy counter.** It needs a migration, and it would not keep production's share free.
  - **Tear previews down inside every delete path.** Apps are deleted from several paths (API, dashboard, project reconcile); the janitor rule covers all of them and earlier orphans.
