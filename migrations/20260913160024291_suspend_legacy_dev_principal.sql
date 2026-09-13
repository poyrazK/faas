-- +goose Up
-- A production control plane must never retain the synthetic local-dev
-- principal. Report row counts without selecting credential material, revoke
-- every usable key, and suspend the account in the same migration.
-- +goose StatementBegin
DO $$
DECLARE
    dev_accounts integer;
    usable_keys integer;
BEGIN
    SELECT count(*) INTO dev_accounts
      FROM accounts
     WHERE email = 'dev@local';

    SELECT count(*) INTO usable_keys
      FROM api_keys k
      JOIN accounts a ON a.id = k.account_id
     WHERE a.email = 'dev@local'
       AND k.status IN ('active', 'grace');

    RAISE NOTICE 'legacy dev principal audit: accounts=%, usable_keys=%',
        dev_accounts, usable_keys;

    UPDATE api_keys k
       SET status = 'revoked',
           revoked_at = COALESCE(k.revoked_at, now()),
           expires_at = LEAST(COALESCE(k.expires_at, now()), now())
      FROM accounts a
     WHERE a.id = k.account_id
       AND a.email = 'dev@local'
       AND k.status IN ('active', 'grace');

    UPDATE accounts
       SET status = 'suspended'
     WHERE email = 'dev@local'
       AND status <> 'suspended';
END $$;
-- +goose StatementEnd

-- +goose Down
-- Security revocation is intentionally irreversible. A local developer can
-- create a new synthetic fixture after rolling back; old keys stay revoked.
SELECT 1;
