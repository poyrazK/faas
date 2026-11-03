# ADR-20261029214500: PR preview binding, names, renames and checks

- **Status:** accepted
- **Date:** 2026-10-29
- **Amends:** ADR-095 (PR preview environments), ADR-20261028100000 (preview lifetime)
- **Decision:** A third code review of PR previews found these gaps; this ADR fixes them.
  1. **A PR finds its binding on any production branch.**
     - githubd looked bindings up without a branch, and both stores read an empty branch as `main`. A repository bound on `master` (or any other production branch) never got a preview, a check or a comment.
     - githubd now tries the PR's base branch, then the project's production branch, then the repository's default branch, then the legacy branch-less lookup. When a repository binds apps on several production branches, the app whose production branch is the PR's base branch is previewed.
  2. **A new service cannot take another app's preview host.** A service that exists only in the pull request is previewed at `pr-<N>-<service>`, the host of `<service>`'s own previews. If any app (in any account) holds that name, the head fails with a setup error asking for a unique service name.
  3. **Renames.**
     - Renaming a production app relinks its PR previews (`preview_of_slug`) in the same transaction. The preview janitor no longer reads them as orphaned. They keep their `pr-<N>-<old>` hosts until the PR's next head reserves `pr-<N>-<new>` and the set replacement retires the old rows.
     - A preview app cannot be renamed (409): its slug is its identity.
     - A GitHub repository rename moves the PR's `pr_preview_sets` rows to the new name. A row already recorded under the new name wins; the old-name row is dropped.
  4. **A torn-down preview stays torn down on close.** A close event for a preview whose row is a tombstone (lease expired, destroyed) does nothing, instead of rewriting the PR comment to say the preview keeps serving for 24 hours.
  5. **No preview check stays pending.**
     - A closed or merged PR's head reports its environment's final result (closed members count as serving) when the build finishes.
     - A commit superseded by a newer head completes as neutral once its deployment is live, failed or cancelled. A deployment that went live as the head already completed its check, so its later supersession is not reported over that.
     - A teardown that cancels an unfinished build completes the commit's check as neutral.
     - An open PR whose head is the same commit still owns the shared check.
- **Why:** Customers on `master` had no previews at all; another account could serve its code at a name it does not own; renaming a production app tore its previews down within minutes; and closing, merging or pushing quickly left checks pending forever.
- **Consequences:**
  - No migration.
  - A repository with several bound apps can now preview a different app than before when the PR's base branch has its own binding.
  - A PR that adds a service named like an existing app must rename the service to get a preview.
- **Rejected alternatives:**
  - **Rename previews' slugs with their production app.** Hosts, snapshot paths and gateway caches key on the slug; replacing the preview on the next head reuses the existing set replacement.
  - **Namespace new-service previews under the root's name.** It changes every existing new-service preview URL and can exceed one DNS label.
