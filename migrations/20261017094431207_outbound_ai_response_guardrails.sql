-- filename: 20261017094431207_outbound_ai_response_guardrails.sql
-- ADR-1400: AI gateway phase 13. The content policy checks model responses:
-- the request detectors redact or block in generated text, blocked terms
-- block it, and the policy's moderation check flags or blocks it. Response
-- moderation uses the stored moderation settings even while requests are
-- not moderated, so those settings must be complete when it is on. Additive
-- and replay-safe.

-- migration-compat: expand
-- +goose Up
SET LOCAL lock_timeout = '5s';
-- +goose StatementBegin
ALTER TABLE outbound_integrations
    ADD COLUMN IF NOT EXISTS ai_response_email text NOT NULL DEFAULT 'off',
    ADD COLUMN IF NOT EXISTS ai_response_payment_card text NOT NULL DEFAULT 'off',
    ADD COLUMN IF NOT EXISTS ai_response_iban text NOT NULL DEFAULT 'off',
    ADD COLUMN IF NOT EXISTS ai_response_secret text NOT NULL DEFAULT 'off',
    ADD COLUMN IF NOT EXISTS ai_response_blocked_terms text NOT NULL DEFAULT 'off',
    ADD COLUMN IF NOT EXISTS ai_response_moderation text NOT NULL DEFAULT 'off';

DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_catalog.pg_constraint
                   WHERE conname = 'outbound_integrations_ai_response_policy_chk' AND conrelid = 'outbound_integrations'::regclass) THEN
        ALTER TABLE outbound_integrations ADD CONSTRAINT outbound_integrations_ai_response_policy_chk
            CHECK (ai_response_email IN ('off', 'redact', 'block')
                   AND ai_response_payment_card IN ('off', 'redact', 'block')
                   AND ai_response_iban IN ('off', 'redact', 'block')
                   AND ai_response_secret IN ('off', 'redact', 'block')
                   AND ai_response_blocked_terms IN ('off', 'block')
                   AND ai_response_moderation IN ('off', 'flag', 'block')
                   AND (ai_provider IS NOT NULL
                        OR (ai_response_email = 'off' AND ai_response_payment_card = 'off' AND ai_response_iban = 'off'
                            AND ai_response_secret = 'off' AND ai_response_blocked_terms = 'off'
                            AND ai_response_moderation = 'off'))
                   AND (ai_response_moderation = 'off'
                        OR (ai_moderation_kind IS NOT NULL
                            AND cardinality(ai_moderation_categories) >= 1
                            AND NOT (ai_moderation_categories <@ ARRAY['prompt_injection']::text[])
                            AND (ai_moderation_kind = 'openai_moderation' OR ai_moderation_model IS NOT NULL))));
    END IF;
END
$$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE outbound_integrations
    DROP CONSTRAINT IF EXISTS outbound_integrations_ai_response_policy_chk,
    DROP COLUMN IF EXISTS ai_response_moderation,
    DROP COLUMN IF EXISTS ai_response_blocked_terms,
    DROP COLUMN IF EXISTS ai_response_secret,
    DROP COLUMN IF EXISTS ai_response_iban,
    DROP COLUMN IF EXISTS ai_response_payment_card,
    DROP COLUMN IF EXISTS ai_response_email;
-- +goose StatementEnd
