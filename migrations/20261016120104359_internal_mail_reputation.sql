-- filename: 20261016120104359_internal_mail_reputation.sql

-- +goose Up
-- +goose StatementBegin
-- Only the authenticated provider ingestion path writes this ledger. First
-- observations never move on replay, even across provider report identities.
CREATE TABLE IF NOT EXISTS internal_mail_reputation_feedback (
    namespace text NOT NULL,
    recipient_id uuid NOT NULL,
    bounced_at timestamptz CHECK (isfinite(bounced_at)),
    complained_at timestamptz CHECK (isfinite(complained_at)),
    bounce_event_id text,
    complaint_event_id text,
    PRIMARY KEY(namespace,recipient_id),
    FOREIGN KEY(namespace,recipient_id) REFERENCES internal_mail_recipients(namespace,id),
    FOREIGN KEY(namespace,bounce_event_id) REFERENCES internal_mail_events(namespace,id),
    FOREIGN KEY(namespace,complaint_event_id) REFERENCES internal_mail_events(namespace,id),
    CHECK ((bounced_at IS NULL)=(bounce_event_id IS NULL)),
    CHECK ((complained_at IS NULL)=(complaint_event_id IS NULL)),
    CHECK (bounced_at IS NOT NULL OR complained_at IS NOT NULL)
);
CREATE INDEX IF NOT EXISTS internal_mail_reputation_feedback_window_idx
    ON internal_mail_reputation_feedback(namespace,(coalesce(complained_at,bounced_at)));
CREATE INDEX IF NOT EXISTS internal_mail_reputation_exposures_idx ON internal_mail_events(namespace,created_at,recipient_id)
    WHERE kind IN ('submitted','uncertain','accepted','bounced','complained');
CREATE TABLE IF NOT EXISTS internal_mail_reputation_holds (
    namespace text NOT NULL CHECK (namespace ~ '^[a-z][a-z0-9_-]{0,63}$'),
    scope text NOT NULL CHECK (scope IN ('producer','domain')),
    scope_key text NOT NULL,
    held boolean NOT NULL,
    revision bigint NOT NULL CHECK (revision>0),
    reason text NOT NULL CHECK (reason IN ('bounce_rate','complaint_rate')),
    samples bigint NOT NULL CHECK (samples>0),
    bounces bigint NOT NULL CHECK (bounces>=0),
    complaints bigint NOT NULL CHECK (complaints>=0),
    window_start timestamptz NOT NULL CHECK (isfinite(window_start)),
    window_end timestamptz NOT NULL CHECK (isfinite(window_end) AND window_end>window_start),
    trigger_recipient_id uuid NOT NULL,
    trigger_event_id text NOT NULL,
    updated_at timestamptz NOT NULL DEFAULT clock_timestamp() CHECK (isfinite(updated_at)),
    PRIMARY KEY(namespace,scope,scope_key),
    CHECK (bounces+complaints<=samples),
    CHECK ((scope='producer' AND (scope_key='' OR scope_key ~ '^[a-z][a-z0-9_-]{0,63}$'))
        OR (scope='domain' AND scope_key ~ '^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$'))
);
CREATE TABLE IF NOT EXISTS internal_mail_reputation_events (
    namespace text NOT NULL CHECK (namespace ~ '^[a-z][a-z0-9_-]{0,63}$'),
    id uuid NOT NULL,
    sequence bigint GENERATED ALWAYS AS IDENTITY,
    scope text NOT NULL CHECK (scope IN ('producer','domain')),
    scope_key text NOT NULL,
    revision bigint NOT NULL CHECK (revision>0),
    held boolean NOT NULL,
    reason text NOT NULL CHECK (reason IN ('bounce_rate','complaint_rate')),
    evidence jsonb NOT NULL CHECK (jsonb_typeof(evidence)='object'),
    request_id uuid,
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    PRIMARY KEY(namespace,id),
    UNIQUE(namespace,sequence),
    UNIQUE(namespace,scope,scope_key,revision)
);
ALTER TABLE internal_mail_operations DROP CONSTRAINT IF EXISTS internal_mail_operations_action_check;
ALTER TABLE internal_mail_operations ADD CONSTRAINT internal_mail_operations_action_check
    CHECK (action IN ('reconcile','retention_apply','dkim_stage','dkim_verify','dkim_activate','dkim_retire','domain_reverify',
                      'delivery_pause','delivery_resume','delivery_drain','message_cancel','recipient_cancel','reputation_release'));
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM information_schema.tables WHERE table_schema=current_schema()
        AND table_name='internal_mail_reputation_feedback') THEN
        LOCK TABLE internal_mail_reputation_feedback IN ACCESS EXCLUSIVE MODE;
        IF EXISTS (SELECT 1 FROM internal_mail_reputation_feedback) THEN
            RAISE EXCEPTION 'internal mail cannot discard authenticated reputation feedback';
        END IF;
    END IF;
    IF EXISTS (SELECT 1 FROM information_schema.tables WHERE table_schema=current_schema()
        AND table_name='internal_mail_reputation_holds') THEN
        LOCK TABLE internal_mail_reputation_holds IN ACCESS EXCLUSIVE MODE;
        IF EXISTS (SELECT 1 FROM internal_mail_reputation_holds) THEN
            RAISE EXCEPTION 'internal mail cannot discard durable reputation holds';
        END IF;
    END IF;
    IF EXISTS (SELECT 1 FROM information_schema.tables WHERE table_schema=current_schema()
        AND table_name='internal_mail_reputation_events') THEN
        LOCK TABLE internal_mail_reputation_events IN ACCESS EXCLUSIVE MODE;
        IF EXISTS (SELECT 1 FROM internal_mail_reputation_events) THEN
            RAISE EXCEPTION 'internal mail cannot discard reputation transition evidence';
        END IF;
    END IF;
    LOCK TABLE internal_mail_operations IN ACCESS EXCLUSIVE MODE;
    IF EXISTS (SELECT 1 FROM internal_mail_operations WHERE action='reputation_release') THEN
        RAISE EXCEPTION 'internal mail cannot discard reputation operator evidence';
    END IF;
END $$;
DROP TABLE IF EXISTS internal_mail_reputation_events;
DROP TABLE IF EXISTS internal_mail_reputation_holds;
DROP TABLE IF EXISTS internal_mail_reputation_feedback;
DROP INDEX IF EXISTS internal_mail_reputation_exposures_idx;
ALTER TABLE internal_mail_operations DROP CONSTRAINT IF EXISTS internal_mail_operations_action_check;
ALTER TABLE internal_mail_operations ADD CONSTRAINT internal_mail_operations_action_check
    CHECK (action IN ('reconcile','retention_apply','dkim_stage','dkim_verify','dkim_activate','dkim_retire','domain_reverify',
                      'delivery_pause','delivery_resume','delivery_drain','message_cancel','recipient_cancel'));
-- +goose StatementEnd
