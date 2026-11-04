-- migration-compat: expand
-- filename: 20261104084723000_egress_overage_choices.sql

-- ADR-20261103131742 amendment 2: a paid billing subject (an account or a
-- company organization) may let its apps keep serving past the plan's
-- monthly egress, billed as overage under its spend cap. Off is the default,
-- and a subject without a row is off. Every change is kept, so an hour used
-- while egress overage was on is billed even when it is turned off before
-- meterd delivers that hour. apid is the only writer; the rows go with the
-- subject's resource namespace when it is erased.

-- +goose Up
SET LOCAL lock_timeout = '5s';

CREATE TABLE IF NOT EXISTS egress_overage_choices (
    subject_id uuid NOT NULL REFERENCES resource_namespaces(id) ON DELETE CASCADE,
    changed_at timestamptz NOT NULL,
    enabled boolean NOT NULL,
    PRIMARY KEY (subject_id, changed_at)
);
COMMENT ON TABLE egress_overage_choices IS 'Each time a paid billing subject turned egress overage on or off; the latest row is the current choice and no row means off. Written only by apid (ADR-20261103131742 amendment 2).';

-- +goose Down
SET LOCAL lock_timeout = '5s';
DROP TABLE IF EXISTS egress_overage_choices;
