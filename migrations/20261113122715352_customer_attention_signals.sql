-- migration-compat: expand
-- +goose Up
SET LOCAL lock_timeout = '5s';
-- Customers who are probably stuck (ADR-20261113123000). apid's evaluator
-- opens, refreshes and resolves rows; support staff may dismiss them.
-- Identifiers only: subject is an app slug, problem code or hostname.
CREATE TABLE IF NOT EXISTS customer_attention_signals (
 id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
 account_id uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
 kind text NOT NULL CHECK(kind IN ('deploy_failing','refused_burst','domain_stuck')),
 subject text NOT NULL CHECK(octet_length(subject) BETWEEN 1 AND 253),
 detail text NOT NULL DEFAULT '' CHECK(octet_length(detail)<=500),
 state text NOT NULL CHECK(state IN ('open','dismissed','resolved')),
 observed bigint NOT NULL CHECK(observed>=0),
 first_seen_at timestamptz NOT NULL CHECK(isfinite(first_seen_at)),
 last_seen_at timestamptz NOT NULL CHECK(isfinite(last_seen_at)),
 resolved_at timestamptz CHECK(resolved_at IS NULL OR isfinite(resolved_at)),
 dismissed_at timestamptz CHECK(dismissed_at IS NULL OR isfinite(dismissed_at)),
 dismissed_by uuid REFERENCES accounts(id) ON DELETE SET NULL,
 dismiss_reason text NOT NULL DEFAULT '' CHECK(octet_length(dismiss_reason)<=200),
 CHECK((state='resolved')=(resolved_at IS NOT NULL)),
 CHECK(state<>'dismissed' OR dismissed_at IS NOT NULL)
);
CREATE UNIQUE INDEX IF NOT EXISTS customer_attention_signals_active_key ON customer_attention_signals(account_id,kind,subject) WHERE state IN ('open','dismissed');
CREATE INDEX IF NOT EXISTS customer_attention_signals_state_idx ON customer_attention_signals(state,last_seen_at DESC);
CREATE INDEX IF NOT EXISTS customer_attention_signals_account_idx ON customer_attention_signals(account_id,state);
CREATE INDEX IF NOT EXISTS customer_attention_signals_resolved_idx ON customer_attention_signals(resolved_at) WHERE state='resolved';

-- +goose Down
SET LOCAL lock_timeout = '5s';
DROP TABLE IF EXISTS customer_attention_signals;
