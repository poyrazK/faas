# Migration backfill removal

The v14 + v19 deploy-time backfill was a one-shot band-aid for prod
DB drift that existed at the moment PR #104 merged. The deploy at
run **29851097984** ran the heredoc end-to-end and recorded both
missing rows in `goose_db_version`. The follow-up PR that removes
the backfill block from `.github/workflows/cd-digitalocean.yml` is
the **prod-clean cleanup** that lets deploys run without carrying
the band-aid.

This runbook is the operator checklist for that cleanup PR. It has
two parts: (a) a precondition check the operator runs on prod before
merging, (b) a rollback recipe in case the static contiguity check
fails in a future PR and the backfill needs to come back.

## Background — why the backfill existed

PR #102 closed a source-side gap in the migration filename sequence
(by renumbering `00020_gdpr_requests.sql` → `00019_gdpr_requests.sql`)
and added a static contiguity check that fails any PR which leaves a
hole in the on-disk migration set. But the new binary was the first
to ship a v19 SQL file while prod's `goose_db_version` had walked
13 → 15 → 20 → 21 without ever seeing v19 on disk — and v14 had
been in the same shape since the PR #83 era. Goose's
`findMissingMigrations` (v3.27.2 `up.go:82-89`) refuses to apply
when the binary embeds a slot the DB is past; `WithAllowMissing()`
is a Go API option, not a CLI flag, so the right place to repair
was the deploy workflow, not `cmd/migrate`. PR #104 added the
`backfill_slot()` heredoc gated by `SELECT EXISTS … WHERE
version_id IN (14, 19)` and the conditional `migrations/*.sql` scp.

**The static check is now the upstream guard.** Future PRs that
renumber a slot fail `make migrations-check` and never reach the
deploy. The backfill block only served the two slots prod was
already missing at PR #104's merge; once those rows are recorded,
the block is dead weight (two `SELECT EXISTS` per deploy + a
~40-line heredoc + a `scp migrations/*.sql`).

## Pre-merge: verify prod is clean

The follow-up PR is safe to merge when prod's `goose_db_version`
has rows for **both** v14 and v19. Confirm:

```sh
ssh root@$DO_HOST
su - faas -s /bin/bash -c \
  "psql -At -d faas -c \"
     SELECT version_id
     FROM goose_db_version
     WHERE version_id IN (14, 19) AND is_applied
     ORDER BY version_id;\""
# Expected output (two rows, one per line):
#   14
#   19
```

If you see both `14` and `19`, merge the follow-up PR. If either is
missing, the backfill isn't done — **do not merge yet**. The
follow-up PR is no-functional-change (just deletions), so the only
way the rows could be missing is if the backfill deploy run failed
halfway. Re-run `cd-digitalocean` from the Actions tab (use
`workflow_dispatch`) to retry the heredoc, then re-check.

Expected deploy log line, if the backfill re-runs:

```
→ Backfilling v14 (00014_cli_auth_codes.sql)
✓ v14 backfilled
→ Backfilling v19 (00019_gdpr_requests.sql)
✓ v19 backfilled
```

If both `✓ v14 backfilled` and `✓ v19 backfilled` lines appear (or
both slots were already recorded and you saw the `already recorded,
skipping` variant), the pre-merge query above will succeed.

## Post-merge: confirm deploy log is clean

The merged follow-up PR removes the backfill heredoc, the
`migrations/*.sql` scp, the `mkdir … deploy-tmp/migrations`, and
all `TODO(remove-once-prod-clean)` markers. The next push-to-main
deploy log will not contain any `→ Backfilling` or `✓ v{N} backfilled`
lines. Expected output:

```
✓ Gateway healthy (apid reachable via loopback proxy)
Gateway healthz: HTTP 200
```

If you see a `→ Backfilling` line on a deploy after the follow-up
PR merges, something is wrong: either prod's `goose_db_version`
lost rows (extremely unlikely — the table isn't written outside
migrate) or the wrong branch was deployed (the merge of an older
PR). Cross-check `git log -1 --pretty=oneline` on the droplet's
checked-out deploy ref.

## Rollback: re-add the backfill block

If a future PR renumbers a migration slot in a way the static
contiguity check misses (or if a migration is added that prod's
DB can't apply without manual intervention), the deploy will fail
on `bin/migrate` with the `findMissingMigrations` error. The
fastest fix is to restore PR #104's heredoc. Two ways, in order of
preference:

### Option A — cherry-pick the backfill commit (preferred)

```sh
git checkout main
git cherry-pick 55aff8d
git push origin main
# Trigger the deploy: Actions → cd-digitalocean → Run workflow
```

PR #104's commit (`55aff8d`) is a single self-contained diff that
adds the heredoc + scp + TODO markers. Cherry-picking it onto a
clean main re-applies the band-aid without dragging in any other
changes. Resolve the merge if main has drifted; the only realistic
conflict is the smoke-test probe URL (`:8080` vs `:8081`) which
was a parallel PR #91 change.

### Option B — git revert the follow-up merge

If cherry-pick conflicts are too messy (e.g., the heredoc now sits
next to a different smoke-test block), revert instead:

```sh
git checkout main
git revert -m 1 <follow-up-merge-sha>
git push origin main
```

`git revert -m 1` keeps main linear and the revert commit is
clearly labelled "revert: rm-backfill-heredoc" — useful for
post-incident review. The cherry-pick is faster; the revert is
safer when you can't predict the merge conflicts.

### Option C — manual SSH repair

If neither git option is acceptable (e.g., the droplet is mid-deploy
and you can't wait for the next push), the backfill is reproducible
by hand:

```sh
ssh root@$DO_HOST
su - faas -s /bin/bash -c \
  "psql -v ON_ERROR_STOP=1 -d faas -f \
     /opt/faas/bin/deploy-tmp/migrations/00014_cli_auth_codes.sql"
su - faas -s /bin/bash -c \
  "psql -v ON_ERROR_STOP=1 -d faas -c \"
     INSERT INTO goose_db_version (version_id, is_applied)
     VALUES (14, true) ON CONFLICT DO NOTHING;\""
# (repeat for v19 / 00019_gdpr_requests.sql)
```

But `/opt/faas/bin/deploy-tmp/` is `rm -rf`'d at the end of every
deploy, so the SQL files won't be there except during the deploy
run that just failed. If you're mid-deploy-recovery, copy them
from `/opt/faas/migrations/` (left behind by older deploys) instead.
Or, cleaner: use `git show origin/main:migrations/000NN_*.sql` from
the Actions runner to extract the SQL on the spot.

## When to reach for the rollback

The static check (`migrations/embed_test.go:97
TestMigrationsContiguous`) fires on any PR that renumbers a slot.
The apply-and-walk test (`migrations/apply_walk_test.go:46
TestMigrationsApplyAndWalk`) is the end-to-end version that runs
against a fresh PG in CI. **Both run on every PR**, so by the time
the deploy sees a gap, both checks have already failed in CI and
the bad PR shouldn't have been merged.

If a gap somehow reaches prod (e.g., a force-push, a direct DB edit
on the droplet, or a future migration applied out-of-band), the
rollout is the same as any other deploy failure: the deploy job
fails at the `bin/migrate` step, no service restart happens, the
droplet keeps running the previous binary. That's by design — the
deploy is `set -euo pipefail` and `install`-only, so a failed
`migrate` aborts before any `systemctl restart` fires. No partial
deploy state.

## References

- PR #102 — `chore(ci): migration-contiguity check + close slot-19 gap`.
  Added the static contiguity check + renumbered `00020_*.sql` →
  `00019_*.sql`. Commit `21cdd4b`.
- PR #104 — `fix(cd): backfill prod v14+v19 in deploy so migrate no
  longer fails`. Added the backfill heredoc. Commit `55aff8d`,
  merged as `1575ad6`. Deploy run `29851097984` exercised it.
- This follow-up PR — `chore(cd): rm deploy-time backfill heredoc
  now that prod is clean`. Deletes the heredoc + scp + mkdir
  segment. Branch name: `chore-rm-backfill-heredoc` (single
  squash-merge commit).
- Plan file: `/.claude/plans/lets-create-imp-plan-joyful-possum.md`.
