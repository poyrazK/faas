# ADR-041 — Migration slot reservation convention

**Superseded by ADR-142 for migrations after version 590.** This ADR remains
the historical contract for the frozen five-digit migration set.

Status: Accepted, 2026-07-28. Owner: @poyrazK. Related: spec §5
(migrations), ADR-039 (server-side session revocation),
`scripts/ci/check_migration_slots.sh` (introduced in PR #377), issue
#366 (main slot-54 collision).

## Context

Three PRs (#335 IAM-3 server-side session revocation, #369 billing:
credit consumption reducer, #352 feat(githubd): real user-to-server
OAuth) opened against `origin/main @ e935b93` in the same week
(2026-07-28) and all added a `00056_*.sql` migration. The cross-PR slot
gate (`scripts/ci/check_migration_slots.sh`, PR #377) caught the
collision at PR time — but it caught it symmetrically. Whichever PR
merged second would still panic goose with "duplicate version 56
detected" and break main (the failure mode PR #377 was written to
prevent; see issue #366 for the slots 51, 52, 54 history).

Two reservation spellings appeared on disk to dodge the symmetric gate:

- `migrations/00056_reserve_slot.sql` (PR #335, commit 40db829)
- `migrations/00056_no_op_slot_reservation.sql` (PR #369 in an
  intermediate state)

Both are `select 1;` no-ops kept only to satisfy
`embed_test.go::TestMigrationsContiguous`, which requires 1..N embedded
migrations with no gaps. Real schemas have to live at 00057, 00058, etc.

The gate's previous behaviour treated every added migration as a slot
claim (`scripts/ci/check_migration_slots.sh` lines 38 + 47 + 71;
`ci.yml` lines 758–759). That was wrong for reservations: a
reservation is a placeholder, not a real schema, and many concurrent
PRs should be allowed to reserve the same slot while only their real
schemas must avoid overlap.

## Decision

**1. Reservation detection rule.** A migration filename is a reservation
if its basename matches (case-insensitive):

```
^migrations/[0-9]{5}_(.*_)?(reservation|reserve_slot)(_[^/]*)?\.sql$
```

Equivalently: a substring of `_reservation` or `_reserve_slot`,
anchored to a non-alphanumeric-or-underscore boundary so
`_reserved_credits` does not match. Accepts both existing patterns
(`reserve_slot`, `no_op_slot_reservation`) and any future spelling,
while rejecting real schema names that share a token
(`credit_consumption`, `reserved_credits`).

**2. Canonical name going forward:** `NNNNN_reserve_slot.sql`. Shorter
than `_reservation` and matches PR #335's existing on-disk file.

**3. Gate carve-out.** `slots_from_paths` in
`scripts/ci/check_migration_slots.sh` and the bash extraction in
`.github/workflows/ci.yml` (the `compute added migration versions`
step) both drop reservation paths BEFORE extracting the slot prefix.
The extracted slot is then excluded from the `comm -12` overlap check
on both `mine` and `theirs` sides.

**4. Embedding is unchanged.** `migrations/embed.go`'s `//go:embed *.sql`
continues to embed reservation files. They are real migrations — goose
applies them, they show up in `goose_db_version`, and they satisfy
`TestMigrationsContiguous`'s 1..N requirement. Removing them would
break contiguity (00055 → 00057 leaves a gap that goose's strict
`findMissingMigrations` refuses to bridge).

**5. The post-merge backstop stays.** `embed_test.go::TestMigrationsUniquePrefixes`
is the second line of defence and remains authoritative: it fires the
moment two real-schema files land at the same slot, regardless of how
many reservations share the prefix.

**6. No new exclusion for `LoadMigrations` in Go.** The Go-side parser
keeps loading every `*.sql` file; it has no concept of "reservation"
because the uniqueness check it supports already covers the
two-real-schemas-at-same-slot case that reservations don't introduce.

**7. Self-test.** A `BATS_TEST=1` env-gated branch at the tail of
`check_migration_slots.sh` exercises the regex against five fixtures
(real schema, both reservation spellings, a second real schema, and a
negative test that must NOT be filtered). The script exits 0 with
`SELF-TEST PASS` and 1 with `SELF-TEST FAIL`. Wired into CI as a
standalone step (`migration slot gate self-test`) so drift between
the regex and this ADR is caught at PR time, not when a misclassified
filename silently lets two real schemas through.

## Consequences

- PR #335's `00056_reserve_slot.sql` no longer trips the cross-PR gate
  against PRs #369 and #352, as long as those PRs also use reservation
  filenames at slot 56 OR renumber their real schemas to 57+.
- Real schemas still collide normally. A PR with
  `00056_credit_consumption.sql` alongside another PR's
  `00056_reserved_credits.sql` fires the gate on the real-schema slot,
  regardless of either file's intent.
- The single source of truth for "is this a reservation" lives in three
  places (`check_migration_slots.sh`, `ci.yml`, and this ADR). Drift
  between the script's regex and the ADR is a bug; the `BATS_TEST=1`
  self-test in the script is the canary.
- Future migration authors may invent new reservation spellings (e.g.
  `_placeholder`) without changing the gate, provided their filename
  includes `_reservation` or `_reserve_slot`. If a different convention
  becomes necessary, this ADR is superseded and the regex widens.

## Rejected alternatives

- **Per-PR reservation filename (`_res_<pr_number>`).** Forces PRs to
  know their own PR number at file-creation time and breaks the
  "renumber to the next free slot" workflow because the reservation
  would have to be renamed too. The substring match is simpler.
- **Reservation recorded in a sidecar JSON file.** Adds a parse path,
  a CI surface, and a docs obligation. The substring match lives in the
  filename itself; anyone reading `ls migrations/` sees the convention.
- **Drop the gate's reservation carve-out and require PR authors to
  coordinate out-of-band.** That was the pre-PR-#377 state and it
  produced the deadlock that broke slots 51, 52, and 54. Reject.
- **A `_reservation` suffix-only rule (must END in `_reservation.sql`).**
  Rejects PR #335's `reserve_slot.sql` for no benefit; the substring
  match accepts both spellings at zero cost.
