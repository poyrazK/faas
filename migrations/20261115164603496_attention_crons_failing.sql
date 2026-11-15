-- filename: 20261115164603496_attention_crons_failing.sql
-- migration-compat: expand
-- Customer attention signals: crons_failing, an enabled HTTP cron whose
-- newest runs all failed. The widened kind CHECK is added under a new name
-- NOT VALID and validated before the old one is dropped. Re-runnable.
-- +goose NO TRANSACTION

-- +goose Up
SET lock_timeout = '5s';

-- +goose StatementBegin
DO $attention$
BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'customer_attention_signals'::regclass AND conname = 'customer_attention_signals_kind_v3_check') THEN
    ALTER TABLE customer_attention_signals ADD CONSTRAINT customer_attention_signals_kind_v3_check
      CHECK (kind IN ('deploy_failing','refused_burst','domain_stuck','app_oom','boot_failing','jobs_failing','payment_failing','limit_pressure','crons_failing')) NOT VALID;
  END IF;
END
$attention$;
-- +goose StatementEnd

ALTER TABLE customer_attention_signals VALIDATE CONSTRAINT customer_attention_signals_kind_v3_check;
ALTER TABLE customer_attention_signals DROP CONSTRAINT IF EXISTS customer_attention_signals_kind_v2_check;

-- +goose Down
SET lock_timeout = '5s';
DELETE FROM customer_attention_signals WHERE kind = 'crons_failing';
-- +goose StatementBegin
DO $attention$
BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'customer_attention_signals'::regclass AND conname = 'customer_attention_signals_kind_v2_check') THEN
    ALTER TABLE customer_attention_signals ADD CONSTRAINT customer_attention_signals_kind_v2_check
      CHECK (kind IN ('deploy_failing','refused_burst','domain_stuck','app_oom','boot_failing','jobs_failing','payment_failing','limit_pressure')) NOT VALID;
  END IF;
END
$attention$;
-- +goose StatementEnd
ALTER TABLE customer_attention_signals VALIDATE CONSTRAINT customer_attention_signals_kind_v2_check;
ALTER TABLE customer_attention_signals DROP CONSTRAINT IF EXISTS customer_attention_signals_kind_v3_check;
