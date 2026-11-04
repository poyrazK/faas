-- filename: 20261023083130145_incident_notes.sql
-- migration-compat: expand
-- ADR-20261022193512 slice 2: an account's notes on a unified incident. An
-- acknowledgement is a note of kind 'acknowledged' with no body, one per
-- account and incident. The incident is keyed by its API id and by exactly
-- one foreign key to its source row, so notes go with the incident.
-- +goose Up
SET LOCAL lock_timeout = '5s';
-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS incident_notes (
    id uuid NOT NULL,
    account_id uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    incident_id text NOT NULL,
    alert_episode_id uuid REFERENCES alert_episodes(id) ON DELETE CASCADE,
    route_incident_id uuid REFERENCES route_monitor_incidents(id) ON DELETE CASCADE,
    status_incident_id uuid REFERENCES status_incidents(public_id) ON DELETE CASCADE,
    kind text NOT NULL,
    body text NOT NULL,
    author text NOT NULL,
    created_at timestamp with time zone NOT NULL,
    CONSTRAINT incident_notes_pkey PRIMARY KEY (id),
    CONSTRAINT incident_notes_kind_check CHECK (kind IN ('acknowledged', 'note')),
    CONSTRAINT incident_notes_body_check CHECK (char_length(body) <= 2000 AND (kind = 'note') = (body <> '')),
    CONSTRAINT incident_notes_author_check CHECK (char_length(author) BETWEEN 1 AND 320),
    CONSTRAINT incident_notes_one_source CHECK (num_nonnulls(alert_episode_id, route_incident_id, status_incident_id) = 1),
    CONSTRAINT incident_notes_id_matches_source CHECK (incident_id = coalesce('alert-' || alert_episode_id::text, 'route-' || route_incident_id::text, 'platform-' || status_incident_id::text))
);
CREATE UNIQUE INDEX IF NOT EXISTS incident_notes_one_ack_idx ON incident_notes (account_id, incident_id) WHERE kind = 'acknowledged';
CREATE INDEX IF NOT EXISTS incident_notes_incident_idx ON incident_notes (account_id, incident_id, created_at);
CREATE INDEX IF NOT EXISTS incident_notes_alert_episode_idx ON incident_notes (alert_episode_id) WHERE alert_episode_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS incident_notes_route_incident_idx ON incident_notes (route_incident_id) WHERE route_incident_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS incident_notes_status_incident_idx ON incident_notes (status_incident_id) WHERE status_incident_id IS NOT NULL;
-- +goose StatementEnd

-- +goose Down
DROP TABLE IF EXISTS incident_notes;
