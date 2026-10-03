-- filename: 20260923102844371_org_activity_outbox.sql

-- +goose Up
-- +goose StatementBegin

-- The organization timeline is a read projection. Keep a durable copy of
-- each activity fact until it has been projected, so apid restarts and
-- transient projection failures do not lose successful mutations.
CREATE TABLE IF NOT EXISTS org_activity_outbox (
    id           bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    org_id       uuid NOT NULL,
    source_type  text NOT NULL,
    source_id    text NOT NULL,
    activity     jsonb NOT NULL CHECK (jsonb_typeof(activity) = 'object'),
    state        text NOT NULL DEFAULT 'pending'
                 CHECK (state IN ('pending', 'processing', 'delivered', 'dead_letter')),
    attempts     integer NOT NULL DEFAULT 0 CHECK (attempts >= 0),
    available_at timestamptz NOT NULL DEFAULT now(),
    claimed_by   text,
    claimed_at   timestamptz,
    lease_until  timestamptz,
    delivered_at timestamptz,
    last_error   text,
    created_at   timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT org_activity_outbox_source_uniq UNIQUE (org_id, source_type, source_id)
);

CREATE INDEX IF NOT EXISTS org_activity_outbox_claim_idx
    ON org_activity_outbox (state, available_at, id)
    WHERE state IN ('pending', 'processing');

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS org_activity_outbox_claim_idx;
DROP TABLE IF EXISTS org_activity_outbox;
-- +goose StatementEnd
