-- Projection of the tables secretd writes (ADR-20261110210000 §3–4),
-- from migration 20261111124927540.
CREATE TABLE secret_grants (
    app_id             uuid        NOT NULL,
    node_id            uuid        NOT NULL,
    granted_by_node_id uuid        NOT NULL,
    granted_at         timestamptz NOT NULL DEFAULT now(),
    expires_at         timestamptz NOT NULL,
    PRIMARY KEY (app_id, node_id)
);

CREATE TABLE secret_open_audit (
    id        bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    node_id   uuid        NOT NULL,
    app_id    uuid        NOT NULL,
    via       text        NOT NULL,
    entries   integer     NOT NULL,
    opened_at timestamptz NOT NULL DEFAULT now()
);
