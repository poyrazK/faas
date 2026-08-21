-- filename: 00323_reserve_slot.sql
-- Reserved slot to keep the migrations directory contiguous for the
-- local embed (CI's TestMigrationsContiguous pins slot N to position
-- N). SAFE-RELEASES Mega PR #1 foundation (issue #976 / ADR-122)
-- lands at slot 00358+00359; the slots 00320-00333 are bridged with
-- no-op fences so the local embed (which only sees this branch's
-- files) stays gap-free. PR #1005 owns slot 00330 (real migration
-- deployment_openapi_snapshots) plus 00331-00333 as further fences;
-- once #1005 merges, the local fences here will be superseded by
-- PR #1005's files on main and can be dropped in a follow-up commit.
-- +goose Up
-- +goose StatementBegin
SELECT 1;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
SELECT 1;
-- +goose StatementEnd
