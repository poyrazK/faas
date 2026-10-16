-- +goose Up
CREATE TABLE IF NOT EXISTS native_issue_ticket_receipts (
 account_id uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
 request_id uuid NOT NULL,
 app_id uuid NOT NULL REFERENCES apps(id) ON DELETE CASCADE,
 issue_id uuid NOT NULL REFERENCES app_issues(id) ON DELETE CASCADE,
 request_hash text NOT NULL CHECK(request_hash ~ '^[a-f0-9]{64}$'),
 state text NOT NULL DEFAULT 'ready' CHECK(state IN ('ready','dispatching','created','linked')),
 claim_id uuid,
 recover_after timestamptz,
 ticket_url text NOT NULL DEFAULT '' CHECK(octet_length(ticket_url)<=512),
 provider_id text NOT NULL DEFAULT '' CHECK(octet_length(provider_id)<=256),
 link_id uuid,
 created_at timestamptz NOT NULL DEFAULT now(),
 updated_at timestamptz NOT NULL DEFAULT now(),
 PRIMARY KEY(account_id,request_id),
 CHECK ((state IN ('ready','dispatching') AND ticket_url='' AND provider_id='' AND link_id IS NULL) OR
        (state='created' AND ticket_url<>'' AND provider_id<>'' AND link_id IS NULL) OR
        (state='linked' AND ticket_url<>'' AND provider_id<>'' AND link_id IS NOT NULL)),
 CHECK (state<>'dispatching' OR (claim_id IS NOT NULL AND recover_after IS NOT NULL))
);

CREATE INDEX IF NOT EXISTS native_issue_ticket_receipts_issue_idx ON native_issue_ticket_receipts(issue_id);

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM native_issue_ticket_receipts) THEN
  RAISE EXCEPTION 'native ticket recovery receipts are in use; preserve them before rollback';
 END IF;
END $$;
DROP TABLE native_issue_ticket_receipts;
-- +goose StatementEnd
