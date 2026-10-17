-- filename: 20261016174046483_internal_mail_suppression_controls.sql

-- +goose Up
-- +goose StatementBegin
-- The existing table remains the active-block projection. Released addresses
-- retain their revision/history here; no release can revive an older recipient.
CREATE TABLE IF NOT EXISTS internal_mail_suppression_state (
    namespace text NOT NULL CHECK (namespace ~ '^[a-z][a-z0-9_-]{0,63}$'),
    address text NOT NULL CHECK (address=lower(address) AND length(address) BETWEEN 3 AND 254),
    revision bigint NOT NULL CHECK (revision>0),
    last_block_revision bigint NOT NULL CHECK (last_block_revision>0 AND last_block_revision<=revision),
    blocked boolean NOT NULL,
    reason text NOT NULL CHECK (reason IN ('manual','bounced','complained')),
    cause text NOT NULL CHECK (cause IN ('legacy','manual_add','release','hard_bounce','complaint')),
    source_kind text NOT NULL CHECK (source_kind IN ('legacy','operator','service_report','postfix','provider')),
    source_id text NOT NULL DEFAULT '' CHECK (source_id='' OR source_id ~ '^[a-z][a-z0-9_-]{0,31}$'),
    recipient_id uuid,
    event_ref text NOT NULL DEFAULT '' CHECK (event_ref='' OR event_ref ~ '^[0-9a-f]{64}$'),
    request_id uuid,
    created_at timestamptz NOT NULL DEFAULT clock_timestamp() CHECK (isfinite(created_at)),
    updated_at timestamptz NOT NULL DEFAULT clock_timestamp() CHECK (isfinite(updated_at)),
    PRIMARY KEY(namespace,address),
    CHECK (reason<>'complained' OR blocked),
    CHECK ((cause='release')=NOT blocked),
    CHECK (NOT blocked OR last_block_revision=revision),
    CHECK ((source_kind='provider')=(source_id<>'')),
    CHECK ((source_kind='operator')=(request_id IS NOT NULL)),
    CHECK ((source_kind IN ('service_report','postfix','provider'))=(recipient_id IS NOT NULL AND event_ref<>'')),
    CHECK ((cause='legacy' AND source_kind='legacy') OR
           (cause IN ('manual_add','release') AND source_kind='operator') OR
           (cause IN ('hard_bounce','complaint') AND source_kind IN ('service_report','postfix','provider')))
);
CREATE TABLE IF NOT EXISTS internal_mail_suppression_events (
    namespace text NOT NULL,
    address text NOT NULL,
    revision bigint NOT NULL CHECK (revision>0),
    blocked boolean NOT NULL,
    reason text NOT NULL CHECK (reason IN ('manual','bounced','complained')),
    cause text NOT NULL CHECK (cause IN ('legacy','manual_add','release','hard_bounce','complaint')),
    source_kind text NOT NULL CHECK (source_kind IN ('legacy','operator','service_report','postfix','provider')),
    source_id text NOT NULL DEFAULT '' CHECK (source_id='' OR source_id ~ '^[a-z][a-z0-9_-]{0,31}$'),
    recipient_id uuid,
    event_ref text NOT NULL DEFAULT '' CHECK (event_ref='' OR event_ref ~ '^[0-9a-f]{64}$'),
    request_id uuid,
    created_at timestamptz NOT NULL DEFAULT clock_timestamp() CHECK (isfinite(created_at)),
    PRIMARY KEY(namespace,address,revision),
    FOREIGN KEY(namespace,address) REFERENCES internal_mail_suppression_state(namespace,address),
    CHECK (reason<>'complained' OR blocked),
    CHECK ((cause='release')=NOT blocked),
    CHECK ((source_kind='provider')=(source_id<>'')),
    CHECK ((source_kind='operator')=(request_id IS NOT NULL)),
    CHECK ((source_kind IN ('service_report','postfix','provider'))=(recipient_id IS NOT NULL AND event_ref<>'')),
    CHECK ((cause='legacy' AND source_kind='legacy') OR
           (cause IN ('manual_add','release') AND source_kind='operator') OR
           (cause IN ('hard_bounce','complaint') AND source_kind IN ('service_report','postfix','provider')))
);
ALTER TABLE internal_mail_recipients ADD COLUMN IF NOT EXISTS suppression_revision bigint NOT NULL DEFAULT 0 CHECK (suppression_revision>=0);

-- Preserve known complaints even if an old writer later replaced their reason
-- with a bounce from another message. Never reinterpret managed/released state.
UPDATE internal_mail_suppressions s SET reason='complained'
WHERE s.reason<>'complained'
  AND NOT EXISTS (SELECT 1 FROM internal_mail_suppression_state x WHERE x.namespace=s.namespace AND x.address=s.address)
  AND EXISTS (SELECT 1 FROM internal_mail_recipients r WHERE r.namespace=s.namespace AND lower(r.address)=s.address
    AND (r.status='complained' OR EXISTS (SELECT 1 FROM internal_mail_events e WHERE e.namespace=r.namespace AND e.recipient_id=r.id AND e.kind='complained')));
INSERT INTO internal_mail_suppression_state(namespace,address,revision,last_block_revision,blocked,reason,cause,source_kind,created_at,updated_at)
SELECT namespace,address,1,1,true,reason,'legacy','legacy',created_at,created_at FROM internal_mail_suppressions ON CONFLICT DO NOTHING;
INSERT INTO internal_mail_suppression_events(namespace,address,revision,blocked,reason,cause,source_kind,created_at)
SELECT namespace,address,revision,blocked,reason,cause,source_kind,updated_at FROM internal_mail_suppression_state
WHERE cause='legacy' AND revision=1 ON CONFLICT DO NOTHING;

ALTER TABLE internal_mail_operations DROP CONSTRAINT IF EXISTS internal_mail_operations_action_check;
ALTER TABLE internal_mail_operations ADD CONSTRAINT internal_mail_operations_action_check
    CHECK (action IN ('reconcile','retention_apply','dkim_stage','dkim_verify','dkim_activate','dkim_retire','domain_reverify',
                      'delivery_pause','delivery_resume','delivery_drain','message_cancel','recipient_cancel','reputation_release','event_feed_replay',
                      'producer_rotate','producer_revoke','producer_suspend','producer_resume',
                      'encryption_activate','encryption_rewrap','encryption_retire','suppression_add','suppression_release'));
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
LOCK TABLE internal_mail_suppressions,internal_mail_recipients,internal_mail_operations IN ACCESS EXCLUSIVE MODE;
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM information_schema.tables WHERE table_schema=current_schema() AND table_name='internal_mail_suppression_state') THEN
        LOCK TABLE internal_mail_suppression_state,internal_mail_suppression_events IN ACCESS EXCLUSIVE MODE;
        IF EXISTS (SELECT 1 FROM internal_mail_suppression_state WHERE cause<>'legacy' OR revision<>1)
            OR EXISTS (SELECT 1 FROM internal_mail_suppression_events WHERE cause<>'legacy' OR revision<>1) THEN
            RAISE EXCEPTION 'internal mail cannot discard suppression revisions or history';
        END IF;
    END IF;
    IF EXISTS (SELECT 1 FROM pg_attribute WHERE attrelid='internal_mail_recipients'::regclass AND attname='suppression_revision' AND NOT attisdropped) THEN
        IF EXISTS (SELECT 1 FROM internal_mail_recipients WHERE suppression_revision<>0) THEN
            RAISE EXCEPTION 'internal mail cannot discard recipient suppression admission revisions';
        END IF;
    END IF;
    IF EXISTS (SELECT 1 FROM internal_mail_operations WHERE action IN ('suppression_add','suppression_release')) THEN
        RAISE EXCEPTION 'internal mail cannot discard suppression operator evidence';
    END IF;
END $$;
DROP TABLE IF EXISTS internal_mail_suppression_events;
DROP TABLE IF EXISTS internal_mail_suppression_state;
ALTER TABLE internal_mail_recipients DROP COLUMN IF EXISTS suppression_revision;
ALTER TABLE internal_mail_operations DROP CONSTRAINT IF EXISTS internal_mail_operations_action_check;
ALTER TABLE internal_mail_operations ADD CONSTRAINT internal_mail_operations_action_check
    CHECK (action IN ('reconcile','retention_apply','dkim_stage','dkim_verify','dkim_activate','dkim_retire','domain_reverify',
                      'delivery_pause','delivery_resume','delivery_drain','message_cancel','recipient_cancel','reputation_release','event_feed_replay',
                      'producer_rotate','producer_revoke','producer_suspend','producer_resume',
                      'encryption_activate','encryption_rewrap','encryption_retire'));
-- +goose StatementEnd
