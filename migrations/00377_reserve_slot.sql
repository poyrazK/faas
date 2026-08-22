-- filename: 00377_reserve_slot.sql
-- Reserved slot to keep the migrations directory contiguous for the
-- local embed (CI's TestMigrationsContiguous pins slot N to position
-- N). Round-24 bridge fence: PR #1036 (multi-host cert fingerprint
-- cluster) claims this slot for 00377_instances_wake_attempt_active_unique.sql
-- but the file is not in this branch's local embed. The real E.2
-- migrations renumbered in round-24 from 00376+00377 to 00378+00379
-- to clear PR #1036's pre-claim that the cross-PR slot precheck
-- caught. SAFE-RELEASES Mega PR #1 (issue #976 / ADR-124) —
-- round-24 contiguity fill on 2026-08-22.
-- +goose Up
-- +goose StatementBegin
SELECT 1;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
SELECT 1;
-- +goose StatementEnd
