-- filename: 20261016110548882_internal_mail_domain_pacing.sql

-- +goose Up
-- +goose StatementBegin
-- A single durable cooldown per namespace and exact recipient domain. This
-- creates no settings, identities or enabled delivery paths.
CREATE TABLE IF NOT EXISTS internal_mail_domain_pacing (
    namespace text NOT NULL CHECK (namespace ~ '^[a-z][a-z0-9_-]{0,63}$'),
    recipient_domain text NOT NULL CHECK (recipient_domain=lower(recipient_domain)
        AND length(recipient_domain) BETWEEN 1 AND 253
        AND recipient_domain ~ '^[a-z0-9][a-z0-9.-]*[a-z0-9]$'),
    next_allowed_at timestamptz NOT NULL CHECK (isfinite(next_allowed_at)),
    updated_at timestamptz NOT NULL DEFAULT now() CHECK (isfinite(updated_at)),
    PRIMARY KEY(namespace,recipient_domain)
);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM information_schema.tables
        WHERE table_schema=current_schema() AND table_name='internal_mail_domain_pacing') THEN
        LOCK TABLE internal_mail_domain_pacing IN ACCESS EXCLUSIVE MODE;
        IF EXISTS (SELECT 1 FROM internal_mail_domain_pacing) THEN
            RAISE EXCEPTION 'internal mail cannot discard durable recipient domain pacing';
        END IF;
    END IF;
END $$;
DROP TABLE IF EXISTS internal_mail_domain_pacing;
-- +goose StatementEnd
