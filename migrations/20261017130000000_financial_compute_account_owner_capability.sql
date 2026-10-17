-- +goose Up
-- ADR-1361. Protocol-1 registration alone proves only app compute support.
-- A current binary attests account support for its exact native incarnation.
-- A legacy upsert omits this field: it cannot change incarnation after an
-- attested owner without violating the constraint and failing daemon startup.
ALTER TABLE financial_compute_owners ADD COLUMN account_compute_incarnation uuid,
 ADD CONSTRAINT financial_compute_owner_account_incarnation_check
 CHECK (account_compute_incarnation IS NULL OR account_compute_incarnation=incarnation);

-- +goose Down
ALTER TABLE financial_compute_owners DROP COLUMN account_compute_incarnation;
