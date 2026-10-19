-- +goose Up
CREATE TABLE IF NOT EXISTS issue_tracker_linear_connections (
 app_id uuid PRIMARY KEY REFERENCES apps(id) ON DELETE CASCADE,
 account_id uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
 revision bigint NOT NULL CHECK(revision>0),
 connected boolean NOT NULL,
 workspace_id uuid,
 team_id uuid,
 viewer_id uuid,
 sealed_credential bytea,
 updated_at timestamptz NOT NULL DEFAULT now(),
 CHECK ((connected AND workspace_id IS NOT NULL AND team_id IS NOT NULL AND viewer_id IS NOT NULL AND octet_length(sealed_credential)>0 AND sealed_credential IS NOT NULL)
 OR (NOT connected AND workspace_id IS NULL AND team_id IS NULL AND viewer_id IS NULL AND sealed_credential IS NULL))
);

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM issue_tracker_linear_connections WHERE connected) THEN
  RAISE EXCEPTION 'disconnect Linear connections before rollback';
 END IF;
END $$;
DROP TABLE issue_tracker_linear_connections;
-- +goose StatementEnd
