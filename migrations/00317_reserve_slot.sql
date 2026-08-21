-- filename: 00317_reserve_slot.sql
-- Reserved slot for PR-B / PR-C follow-up migrations in the
-- api-contract-diff cluster (ADR-121). Fence-fill rule from
-- PR-992 cycle: every skipped slot before an open-PR's
-- claimed slot must be fenced. PR-992 owns 00318/00319, so
-- this slot (00317) is the immediate predecessor and must
-- not be omitted. Drop in a follow-up commit if the slot is
-- not consumed.
-- +goose Up
-- +goose StatementBegin
SELECT 1;
-- +goose StatementEnd
-- +goose Down
-- +goose StatementBegin
SELECT 1;
-- +goose StatementEnd
