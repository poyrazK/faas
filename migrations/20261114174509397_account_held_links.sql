-- filename: 20261114174509397_account_held_links.sql
-- migration-compat: expand
-- Each step is its own statement, so the session index builds concurrently.
-- Re-runnable after a partial run.
-- +goose NO TRANSACTION

-- +goose Up
-- ADR-20261114174600: a new account whose sign-up shares a network or a
-- private email domain with an abuse-held account is linked to it, for
-- operator review. Flag only: a link never blocks or holds anything. Links
-- are deleted with either account.
SET lock_timeout = '5s';
SET statement_timeout = 0;

CREATE TABLE IF NOT EXISTS account_held_links (
 account_id uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
 held_account_id uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
 held_org_id uuid REFERENCES orgs(id) ON DELETE CASCADE,
 held_reason text NOT NULL CHECK (held_reason IN ('egress_fanout', 'egress_flood', 'cpu_sustained', 'operator')),
 signal text NOT NULL CHECK (signal IN ('signup_network', 'email_domain')),
 detected_at timestamptz NOT NULL CHECK (isfinite(detected_at)),
 PRIMARY KEY (account_id, held_account_id, signal),
 CHECK (account_id <> held_account_id)
);
CREATE INDEX IF NOT EXISTS account_held_links_held_idx ON account_held_links (held_account_id);

-- A held account's sign-in addresses, revoked sessions included, are looked
-- up by account; the existing index covers only unrevoked sessions.
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS (SELECT 1 FROM pg_index i JOIN pg_class c ON c.oid = i.indexrelid
            WHERE c.relname = 'sessions_account_issued_idx' AND NOT i.indisvalid) THEN
  -- A failed concurrent build leaves an invalid index that IF NOT EXISTS
  -- would keep; drop it so a rerun builds it again.
  EXECUTE 'DROP INDEX sessions_account_issued_idx';
 END IF;
END $$;
-- +goose StatementEnd

CREATE INDEX CONCURRENTLY IF NOT EXISTS sessions_account_issued_idx ON sessions (account_id, issued_at DESC);

-- +goose Down
SET lock_timeout = '5s';
DROP INDEX CONCURRENTLY IF EXISTS sessions_account_issued_idx;
DROP TABLE IF EXISTS account_held_links;
