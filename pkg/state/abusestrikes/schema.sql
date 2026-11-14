-- Offline projection of the migration-owned account_abuse_strikes table
-- (migrations/20261113133118913_account_abuse_strikes.sql).
CREATE TABLE account_abuse_strikes (
 id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
 account_id uuid NOT NULL,
 reason text NOT NULL,
 app_id uuid,
 struck_at timestamptz NOT NULL
);
