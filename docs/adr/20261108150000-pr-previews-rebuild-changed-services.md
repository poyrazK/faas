# ADR-20261108150000: A PR push rebuilds only the preview services whose files changed

- Status: accepted
- Date: 2026-11-08
- Amends: ADR-20261108140000 (rejected alternative "rebuilding only the
  preview services whose files changed"), ADR-20261028100000 (PR preview
  build share)

## Context

Every push to a pull request rebuilt every service in its preview set, even
when the push touched one service's directory. Each build takes a deploy from
the PR preview share of the hourly limit, so a multi-service project spent
its share on rebuilds that changed nothing and, under ADR-20261108140000,
failed setup sooner. Production pushes already rebuild only the project apps
whose source changed.

A preview set was live only when every member had a live deployment at the
set's recorded head commit (`previewset.Evaluate`, the PR check and the
customer projection), which is why ADR-20261108140000 rejected skipping
members.

## Decision

1. On a `synchronize` event, githubd compares the previous head with the new
   one and applies the production push's path filter: a service rebuilds when
   a changed file lies in its directory, and every service rebuilds when a
   changed file lies outside all of them. Ignored paths are left out.
2. A service is kept only when it also has a live preview deployment at the
   commit the previous head expected of it, its preview app is open and
   active (or evicted cold), and the same preview app is reserved for the new
   head. Anything uncertain rebuilds everything: an opened or reopened PR, a
   previous head that failed setup, a compare GitHub cannot answer exactly,
   a store without the preview set, or a set where every service would be
   kept.
3. `pr_preview_sets` gains `member_pins` (app id → commit) and
   `member_pins_head`. githubd pins the kept services after reserving the new
   head; a pin succeeds only while that head is the set's open head and every
   pinned app is a member. A failed pin rebuilds everything.
4. Readers (the PR check, the customer projection and the next push's
   filter) expect a pinned member at its pinned commit, every other member at
   the head. Pins apply only while `member_pins_head` equals `commit_sha`, so
   the previous release, which moves `commit_sha` without knowing the pins,
   leaves them inert. Every write of a new head clears them.
5. A kept service keeps the configuration of the build it serves, including
   its `GREGALE_GIT_COMMIT`. The deploy-share pre-check counts only the
   services that rebuild.

## Consequences

- A push touching one service spends one deploy, and the others keep serving
  without a restart.
- A kept service reports the commit it was built from, not the PR head.
- The deploy-share pre-check runs before the pin. When the pin then fails,
  githubd rebuilds every service, and the bridge's per-build admission is the
  only gate on the extra builds. The pin fails only when the set changed
  between reservation and pin.
- The migration is expand-only: nullable columns and CHECKs added `NOT VALID`
  and validated separately.

## Alternatives rejected

- Pinning by workload name. Names can be reused by a different preview app
  across heads; app ids cannot.
- Recording the commit per deployment instead of per set. The readers already
  resolve members from the set; a per-set map keeps one source of truth and
  one guard (`member_pins_head`) for release skew.
