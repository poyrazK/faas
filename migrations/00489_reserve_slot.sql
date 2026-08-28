-- filename: 00489_reserve_slot.sql
-- ADR-119 v2 fence. The 00488 migration adds node_id to
-- provisioned_static_egress_ips; PR-A holds 00488 + this 00489
-- fence as the v2 multi-node static egress IP reservation.
-- Pre-claim against origin/main per
-- [[cross-pr-slot-precheck-pr-867-collision-2026-08-13]]: the
-- slot-gate precheck scans 00488+1 for any open PR fence that
-- would collide with a rebase. If a sibling PR lands a
-- migration at 00489, PR-A renumbers to 00490 + drops this
-- fence (per [[cross-pr-rebase-fence-deletion-hazard]]).
-- +goose Up
-- +goose StatementBegin
SELECT 1;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
SELECT 1;
-- +goose StatementEnd