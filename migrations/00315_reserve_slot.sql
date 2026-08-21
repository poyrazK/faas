-- filename: 00315_reserve_slot.sql
-- Reserved slot for PR-B / PR-C follow-up migrations in the
-- api-contract-diff cluster (ADR-121). Reservation fence per
-- ADR-041; the cross-PR slot gate (check_migration_slots.sh)
-- whitelists *reservation.sql filenames so the fence does not
-- count as a real migration. Drop in a follow-up commit if the
-- slot is not consumed.
-- +goose Up
-- +goose StatementBegin
SELECT 1;
-- +goose StatementEnd
-- +goose Down
-- +goose StatementBegin
SELECT 1;
-- +goose StatementEnd
