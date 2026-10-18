-- +goose Up
-- +goose StatementBegin
SET LOCAL lock_timeout = '5s';
-- ADR-1640. A distinct account book prevents an
-- app UUID from being used as the shared allowance identity.
CREATE TABLE IF NOT EXISTS financial_compute_account_books (
 account_id uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
 period_start timestamptz NOT NULL,
 period_end timestamptz NOT NULL CHECK (period_end > period_start),
 payload jsonb NOT NULL CHECK (jsonb_typeof(payload)='object'),
 CHECK (COALESCE((payload->>'committed')::numeric >= 0 AND (payload->>'reserved')::numeric >= 0
   AND (payload->>'committed')::numeric+(payload->>'reserved')::numeric <= 9007199254740991,false)),
 CHECK (COALESCE((payload->>'quantity_committed')::numeric >= 0 AND (payload->>'quantity_reserved')::numeric >= 0
   AND (payload->>'quantity_committed')::numeric+(payload->>'quantity_reserved')::numeric <= 9007199254740991
   AND (payload->>'allowance_granted')::numeric BETWEEN 0 AND 9007199254740991,false)),
 PRIMARY KEY(account_id,period_start)
);
CREATE INDEX IF NOT EXISTS financial_compute_account_active ON financial_compute_leases(account_id) WHERE settled_at IS NULL;
DO $$ BEGIN
 IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid='financial_compute_leases'::regclass AND conname='financial_compute_account_quantity_check') THEN
  ALTER TABLE financial_compute_leases ADD CONSTRAINT financial_compute_account_quantity_check CHECK (
 COALESCE((payload->>'account_funded')::boolean,false)=false OR
 (strict AND COALESCE((payload->>'quantity_committed')::numeric>=0 AND (payload->>'quantity_reserved')::numeric>=0
 AND (payload->>'quantity_committed')::numeric+(payload->>'quantity_reserved')::numeric<=9007199254740991,false)));
 END IF;
END $$;


CREATE OR REPLACE FUNCTION financial_app_command_held(target_app uuid, target_deployment uuid, observed_at timestamptz)
RETURNS boolean LANGUAGE sql STABLE AS $$
 SELECT financial_preview_command_held(target_app,target_deployment,observed_at) OR EXISTS(
 SELECT 1 FROM apps a JOIN financial_budget_policies p ON p.account_id=a.account_id
 JOIN financial_compute_holds h ON h.account_id=p.account_id AND h.policy_id=p.id AND h.revision=p.revision
 WHERE a.id=target_app AND p.deleted_at IS NULL AND p.spec->>'enabled'='true'
  AND p.spec->>'mode'='strict' AND (p.spec->'scope'->>'kind'='account'
    OR p.spec->'scope'->>'kind'='app' AND p.spec->'scope'->>'id'=a.id::text)
  AND (p.spec->>'resume_rule'='manual' OR h.period_start=date_trunc('month',observed_at AT TIME ZONE 'UTC') AT TIME ZONE 'UTC'))
$$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION financial_app_command_held(target_app uuid, target_deployment uuid, observed_at timestamptz)
RETURNS boolean LANGUAGE sql STABLE AS $$
 SELECT financial_preview_command_held(target_app,target_deployment,observed_at) OR EXISTS(
 SELECT 1 FROM apps a JOIN financial_budget_policies p ON p.account_id=a.account_id
 JOIN financial_compute_holds h ON h.account_id=p.account_id AND h.policy_id=p.id AND h.revision=p.revision
 WHERE a.id=target_app AND p.deleted_at IS NULL AND p.spec->>'enabled'='true'
  AND p.spec->>'mode'='strict' AND p.spec->'scope'->>'kind'='app' AND p.spec->'scope'->>'id'=a.id::text
  AND (p.spec->>'resume_rule'='manual' OR h.period_start=date_trunc('month',observed_at AT TIME ZONE 'UTC') AT TIME ZONE 'UTC'))
$$;
-- +goose StatementEnd
ALTER TABLE financial_compute_leases DROP CONSTRAINT financial_compute_account_quantity_check;
DROP INDEX financial_compute_account_active;
DROP TABLE financial_compute_account_books;

-- migration-compat: contract
-- Financial funding rollout coordinates scheduler, native-owner and meter
-- capabilities with the USD gate; this batch requires a full release.
