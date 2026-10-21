# Merge queue for `main` (prepared, not applied)

Decision: [ADR 20261020214632](../adr/20261020214632-automatic-unit-deploy-safety-rails.md) §1.

## Status

Not applied. GitHub offers merge queues only for repositories owned by an
organization (public ones, or private ones on Enterprise Cloud).
poyrazK/faas is owned by a user account, and on 2026-10-20 the owner chose
to keep it that way. Until a queue is on, the deploy pipeline uses the
fallback: a deploy does not start while the commit's post-merge light run
has failed, and the post-deploy watch rolls back when that run fails later.

Everything else is in place: `ci-light.yml` runs on `merge_group` under the
six required check names, with the merge group's parent as its diff base
(`scripts/ci/light_diff_base.sh`).

## Applying it (owner only, after the repository moves to an organization)

This changes a repository setting. Only the owner applies it.

1. Read the live `main` ruleset and keep a copy:

   ```bash
   gh api repos/OWNER/faas/rulesets --jq '.[] | select(.name == "main") | .id'
   gh api repos/OWNER/faas/rulesets/RULESET_ID > main-ruleset.before.json
   ```

2. Save this rule as `merge-queue-rule.json`. It is added to the ruleset's
   `rules` array and nothing else changes:
   `strict_required_status_checks_policy` stays `false`, because the queue
   tests every entry on top of main and doesn't need up-to-date branches.

   ```json
   {
     "type": "merge_queue",
     "parameters": {
       "merge_method": "SQUASH",
       "grouping_strategy": "ALLGREEN",
       "max_entries_to_build": 5,
       "max_entries_to_merge": 5,
       "min_entries_to_merge": 1,
       "min_entries_to_merge_wait_minutes": 5,
       "check_response_timeout_minutes": 60
     }
   }
   ```

   Why these values:

   - `ALLGREEN`: a group merges only when every entry passed.
   - Five builds at once: the busiest day of 2026-10-14 to 10-20 had 67
     merges, about three an hour, and a light run took 14 to 45 min on main.
   - The 60 min timeout covers a hosted-runner queue on a busy day.

3. Write the ruleset back (`PUT` takes the whole ruleset):

   ```bash
   jq --slurpfile rule merge-queue-rule.json \
     '{name, target, enforcement, conditions, bypass_actors, rules: (.rules + $rule)}' \
     main-ruleset.before.json > main-ruleset.after.json
   gh api -X PUT repos/OWNER/faas/rulesets/RULESET_ID --input main-ruleset.after.json
   ```

4. Check it: queue one small PR (`gh pr merge N --squash`; with a queue this
   enqueues it). The PR's checks page shows a `merge_group` run of ci-light
   with the six required checks, and the PR merges when they pass.

## Rolling back

Write `main-ruleset.before.json` back with the same `PUT`. PRs already in
the queue go back to the plain merge button.

## Things to know

- The light tier's PR-only checks (fix-has-test, migration-version
  hygiene) skip on `merge_group`, as they do on pushes. The pull request's
  own run already ran them.
- A merge-group run is never cancelled. A cancelled required check takes the
  entry out of the queue.
- `images.yml` also runs on `merge_group`. No other workflow reports a
  required check.
