-- +goose Up
CREATE TABLE IF NOT EXISTS issue_tracker_callback_diagnostics (
 app_id uuid PRIMARY KEY REFERENCES issue_tracker_linear_connections(app_id) ON DELETE CASCADE,
 account_id uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
 connection_revision bigint NOT NULL CHECK(connection_revision>0),
 invalid_signature bigint NOT NULL DEFAULT 0 CHECK(invalid_signature>=0),
 wrong_scope bigint NOT NULL DEFAULT 0 CHECK(wrong_scope>=0),
 stale_delivery bigint NOT NULL DEFAULT 0 CHECK(stale_delivery>=0),
 invalid_payload bigint NOT NULL DEFAULT 0 CHECK(invalid_payload>=0),
 delivery_conflict bigint NOT NULL DEFAULT 0 CHECK(delivery_conflict>=0),
 last_verified_at timestamptz,
 last_rejected_at timestamptz
);

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM issue_tracker_callback_diagnostics) THEN
  RAISE EXCEPTION 'preserve callback diagnostics before rollback';
 END IF;
END $$;
DROP TABLE issue_tracker_callback_diagnostics;
-- +goose StatementEnd
