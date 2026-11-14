-- migration-compat: expand
-- Customer attention signals, round 2 (ADR-20261113180000): five more
-- kinds, and what support did about each signal. Contacting a customer
-- dismisses the signal with outcome 'contacted' and links the ticket;
-- dismissing names an outcome; the evaluator marks a signal that clears
-- without one 'resolved_on_own'. The widened kind CHECK is added under a
-- new name NOT VALID and validated before the old one is dropped; new
-- columns are added bare and constrained NOT VALID, then validated. Every
-- step is re-runnable.
-- +goose NO TRANSACTION

-- +goose Up
SET lock_timeout = '5s';

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
ALTER TABLE customer_attention_signals DROP CONSTRAINT IF EXISTS customer_attention_signals_kind_check;

ALTER TABLE customer_attention_signals ADD COLUMN IF NOT EXISTS outcome text;
ALTER TABLE customer_attention_signals ADD COLUMN IF NOT EXISTS ticket_id uuid;

-- +goose StatementBegin
DO $attention$
BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'customer_attention_signals'::regclass AND conname = 'customer_attention_signals_outcome_check') THEN
    ALTER TABLE customer_attention_signals ADD CONSTRAINT customer_attention_signals_outcome_check
      CHECK (outcome IS NULL OR outcome IN ('contacted','platform_issue','already_in_touch','not_needed','false_positive','resolved_on_own')) NOT VALID;
  END IF;
  IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'customer_attention_signals'::regclass AND conname = 'customer_attention_signals_contact_ticket_check') THEN
    ALTER TABLE customer_attention_signals ADD CONSTRAINT customer_attention_signals_contact_ticket_check
      CHECK ((outcome IS NOT DISTINCT FROM 'contacted') = (ticket_id IS NOT NULL)) NOT VALID;
  END IF;
END
$attention$;
-- +goose StatementEnd

ALTER TABLE customer_attention_signals VALIDATE CONSTRAINT customer_attention_signals_outcome_check;
ALTER TABLE customer_attention_signals VALIDATE CONSTRAINT customer_attention_signals_contact_ticket_check;

-- +goose Down
SET lock_timeout = '5s';
ALTER TABLE customer_attention_signals DROP CONSTRAINT IF EXISTS customer_attention_signals_contact_ticket_check;
ALTER TABLE customer_attention_signals DROP CONSTRAINT IF EXISTS customer_attention_signals_outcome_check;
ALTER TABLE customer_attention_signals DROP COLUMN IF EXISTS ticket_id;
ALTER TABLE customer_attention_signals DROP COLUMN IF EXISTS outcome;
DELETE FROM customer_attention_signals WHERE kind NOT IN ('deploy_failing','refused_burst','domain_stuck');
-- +goose StatementBegin
DO $attention$
BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'customer_attention_signals'::regclass AND conname = 'customer_attention_signals_kind_check') THEN
    ALTER TABLE customer_attention_signals ADD CONSTRAINT customer_attention_signals_kind_check
      CHECK (kind IN ('deploy_failing','refused_burst','domain_stuck')) NOT VALID;
  END IF;
END
$attention$;
-- +goose StatementEnd
ALTER TABLE customer_attention_signals DROP CONSTRAINT IF EXISTS customer_attention_signals_kind_v2_check;
