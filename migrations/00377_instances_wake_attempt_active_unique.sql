-- filename: 00377_instances_wake_attempt_active_unique.sql
-- +goose Up
-- +goose StatementBegin
--
-- 00377_instances_wake_attempt_active_unique.sql — multi-host safety cluster
-- PR-5 / audit F4 (cluster-wide wakeCoord). Adds a UNIQUE partial index on
-- instances(wake_id) WHERE state IN ('WAKING', 'COLD_BOOTING'). This is the
-- DB-level dedup primitive that closes the cross-box race where two schedd
-- daemons (on different boxes) both boot the same wake attempt: the second
-- INSERT lands a 23505 (UNIQUE violation) and the engine can recover via
-- ReadActiveInstanceForWakeID.
--
-- Why the partial predicate: an instance's wake_id stays stable for the
-- lifetime of that boot. The same wake_id reappearing with state='RUNNING'
-- is the SAME instance, not a duplicate — the engine updated the state
-- column in place. The partial predicate matches ONLY the in-flight states
-- where two rows with the same wake_id would be a true race.
--
-- Why we don't add a NOT NULL constraint on wake_id: the column has been
-- NOT NULL since migrations/00028_instances_wake_id.sql. Re-asserting it
-- here would be a no-op on a fresh DB and could break a half-migrated box
-- (which the project tolerates via the COALESCE pattern in scanInstance).
--
-- Existing non-unique index instances_wake_id_app_idx (00028, on
-- (app_id, wake_id)) is unchanged — it's the chooser-side lookup, not
-- the dedup gate. The two indexes share the wake_id column but serve
-- different planner roles: the new unique partial index enforces the
-- invariant; the legacy non-unique index supports the existing scan
-- pattern.
--
-- Pre-condition: at the time this migration runs, no two rows exist
-- with the same wake_id AND state IN ('WAKING', 'COLD_BOOTING'). This
-- invariant holds for every shipped pre-PR-5 install because (a) on
-- single-box, schedd mints a fresh UUIDv7 per wake; (b) on multi-box
-- without the owner-gate, races COULD have produced duplicates, but
-- the post-restart recovery flow (MarkComputeNodeInactive + cold
-- boot) clears in-flight rows for any drained box before another box
-- picks up the wake. The PR-5 owner gate + the partial index together
-- make the invariant permanent.
--
-- Slot note: PR-5 originally claimed slot 00350 on the
-- multi-host-f6-cert-fingerprint branch (post-00346 tip). Sibling
-- PRs #1019 / #1030 / #1034 landed on main first and consumed
-- slots 00347 + 00350, then PR #1017 consumed 00368 + 00369
-- (its alert preset work), so PR-5 renumbered to 00377 (past
-- PR-4's 00376_compute_nodes_active_unique). PR-4 + PR-5 are
-- the only two real migrations on this branch; no fences needed
-- past them. See docs/runbooks/migration-slot-dance.md.
CREATE UNIQUE INDEX IF NOT EXISTS instances_wake_attempt_active_idx
    ON instances(wake_id)
    WHERE state IN ('WAKING', 'COLD_BOOTING');

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS instances_wake_attempt_active_idx;
-- +goose StatementEnd
