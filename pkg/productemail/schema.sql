-- Projection of migration 20261017185613541; cross-domain FKs live in the migration.
CREATE TABLE product_control_email_subscriptions (
 id uuid PRIMARY KEY,
 account_id uuid NOT NULL,
 app_id uuid NOT NULL,
 tenant_id uuid NOT NULL,
 auth_app_id uuid NOT NULL CHECK(auth_app_id<>'00000000-0000-0000-0000-000000000000'),
 organization_id uuid NOT NULL CHECK(organization_id<>'00000000-0000-0000-0000-000000000000'),
 actor_id uuid NOT NULL CHECK(actor_id<>'00000000-0000-0000-0000-000000000000'),
 app_slug text NOT NULL CHECK(octet_length(app_slug) BETWEEN 1 AND 63),
 auth_slug text NOT NULL CHECK(octet_length(auth_slug) BETWEEN 1 AND 63),
 version bigint NOT NULL CHECK(version BETWEEN 1 AND 1000000),
 work_outcomes boolean NOT NULL,
 help_replies boolean NOT NULL,
 work_since timestamptz NOT NULL CHECK(isfinite(work_since)),
 help_since timestamptz NOT NULL CHECK(isfinite(help_since)),
 expires_at timestamptz,
 grant_sealed bytea NOT NULL CHECK(octet_length(grant_sealed)<=4096),
 scan_cursor text NOT NULL CHECK(octet_length(scan_cursor)<=1024),
 CHECK((work_outcomes OR help_replies) = (expires_at IS NOT NULL)),
 CHECK(expires_at IS NULL OR (isfinite(expires_at) AND octet_length(grant_sealed)>0)),
 UNIQUE(account_id,app_id,tenant_id,auth_app_id,organization_id,actor_id)
);
CREATE TABLE product_control_email_deliveries (
 id uuid PRIMARY KEY,
 subscription_id uuid NOT NULL REFERENCES product_control_email_subscriptions(id) ON DELETE CASCADE,
 preference_version bigint NOT NULL CHECK(preference_version BETWEEN 1 AND 1000000),
 source_revision text NOT NULL CHECK(source_revision ~ '^[a-f0-9]{64}$'),
 notification jsonb NOT NULL CHECK(jsonb_typeof(notification)='object'),
 status text NOT NULL CHECK(status IN ('queued','leased','captured','provider_accepted','failed','cancelled')),
 reason text NOT NULL CHECK(reason IN ('','access_changed','preferences_changed','source_changed','authorization_unavailable','transport_unavailable','provider_rejected','retry_exhausted','expired')),
 attempts integer NOT NULL CHECK(attempts BETWEEN 0 AND 6),
 created_at timestamptz NOT NULL CHECK(isfinite(created_at)),
 updated_at timestamptz NOT NULL CHECK(isfinite(updated_at)),
 next_at timestamptz NOT NULL CHECK(isfinite(next_at)),
 deadline timestamptz NOT NULL CHECK(isfinite(deadline) AND deadline>created_at),
 UNIQUE(subscription_id,source_revision)
);
CREATE INDEX product_control_email_subscribers ON product_control_email_subscriptions(id) WHERE work_outcomes OR help_replies;
CREATE INDEX product_control_email_due ON product_control_email_deliveries(next_at,id) WHERE status IN ('queued','leased');
CREATE INDEX product_control_email_recent ON product_control_email_deliveries(subscription_id,created_at DESC,id DESC);
CREATE INDEX product_control_email_expiry ON product_control_email_deliveries(created_at,id);
CREATE TABLE product_control_email_retry_receipts (
 delivery_id uuid NOT NULL REFERENCES product_control_email_deliveries(id) ON DELETE CASCADE,
 failed_attempt integer NOT NULL CHECK(failed_attempt BETWEEN 1 AND 5),
 queued_at timestamptz NOT NULL CHECK(isfinite(queued_at)),
 PRIMARY KEY(delivery_id,failed_attempt)
);
CREATE INDEX product_control_email_owner_scope ON product_control_email_subscriptions(account_id,app_id,tenant_id,id);

ALTER TABLE product_control_email_subscriptions
 ADD COLUMN pause_reason text NOT NULL DEFAULT '' CHECK(pause_reason IN ('','hard_bounce','complaint')),
 ADD COLUMN paused_at timestamptz CHECK(paused_at IS NULL OR isfinite(paused_at)),
 ADD CONSTRAINT product_email_pause_shape CHECK((pause_reason='')=(paused_at IS NULL));
ALTER TABLE product_control_email_deliveries
 DROP CONSTRAINT product_control_email_deliveries_status_check,
 DROP CONSTRAINT product_control_email_deliveries_reason_check,
 ADD CONSTRAINT product_control_email_deliveries_status_check CHECK(status IN ('queued','leased','captured','provider_accepted','delivered','bounced','complained','failed','cancelled')),
 ADD CONSTRAINT product_control_email_deliveries_reason_check CHECK(reason IN ('','access_changed','preferences_changed','source_changed','authorization_unavailable','transport_unavailable','provider_rejected','retry_exhausted','expired','recipient_suppressed')),
 ADD COLUMN provider_id uuid UNIQUE,
 ADD COLUMN delivered_at timestamptz CHECK(delivered_at IS NULL OR isfinite(delivered_at)),
 ADD COLUMN bounced_at timestamptz CHECK(bounced_at IS NULL OR isfinite(bounced_at)),
 ADD COLUMN complained_at timestamptz CHECK(complained_at IS NULL OR isfinite(complained_at)),
 ADD COLUMN hard_bounced boolean NOT NULL DEFAULT false,
 ADD CONSTRAINT product_email_feedback_status_shape CHECK((status<>'delivered' OR delivered_at IS NOT NULL) AND (status<>'bounced' OR bounced_at IS NOT NULL) AND (status<>'complained' OR complained_at IS NOT NULL)),
 ADD CONSTRAINT product_email_feedback_shape CHECK((provider_id IS NOT NULL OR (delivered_at IS NULL AND bounced_at IS NULL AND complained_at IS NULL)) AND (NOT hard_bounced OR bounced_at IS NOT NULL));
CREATE TABLE product_control_email_pending_feedback (
 provider_id uuid NOT NULL,
 kind text NOT NULL CHECK(kind IN ('delivered','bounced','complained')),
 occurred_at timestamptz NOT NULL CHECK(isfinite(occurred_at)),
 hard_bounce boolean NOT NULL CHECK(NOT hard_bounce OR kind='bounced'),
 received_at timestamptz NOT NULL CHECK(isfinite(received_at)),
 PRIMARY KEY(provider_id,kind)
);
CREATE INDEX product_control_email_pending_feedback_expiry ON product_control_email_pending_feedback(received_at,provider_id,kind);


ALTER TABLE product_control_email_subscriptions
 ADD COLUMN recipient_generation bigint NOT NULL DEFAULT 1 CHECK(recipient_generation BETWEEN 1 AND 1000000),
 ADD COLUMN pause_recipient_sealed bytea NOT NULL DEFAULT '' CHECK(octet_length(pause_recipient_sealed)<=4096),
 ADD CONSTRAINT product_email_pause_recipient_shape CHECK(pause_reason<>'' OR octet_length(pause_recipient_sealed)=0);
ALTER TABLE product_control_email_deliveries
 ADD COLUMN recipient_generation bigint NOT NULL DEFAULT 1 CHECK(recipient_generation BETWEEN 1 AND 1000000),
 ADD COLUMN recipient_sealed bytea NOT NULL DEFAULT '' CHECK(octet_length(recipient_sealed)<=4096);
CREATE TABLE product_control_email_recoveries (
 id uuid PRIMARY KEY CHECK(id<>'00000000-0000-0000-0000-000000000000'),
 subscription_id uuid NOT NULL REFERENCES product_control_email_subscriptions(id) ON DELETE CASCADE,
 expected_version bigint NOT NULL CHECK(expected_version BETWEEN 1 AND 999999),
 previous_generation bigint NOT NULL CHECK(previous_generation BETWEEN 1 AND 999999),
 version bigint NOT NULL CHECK(version=expected_version+1),
 generation bigint NOT NULL CHECK(generation=previous_generation+1),
 work_outcomes boolean NOT NULL,
 help_replies boolean NOT NULL,
 recovered_at timestamptz NOT NULL CHECK(isfinite(recovered_at)),
 CONSTRAINT product_email_recovery_consent CHECK(work_outcomes OR help_replies),
 UNIQUE(subscription_id,previous_generation)
);
CREATE INDEX product_email_recovery_recent ON product_control_email_recoveries(subscription_id,recovered_at DESC,id DESC);
CREATE INDEX product_email_recovery_expiry ON product_control_email_recoveries(recovered_at,id);


ALTER TABLE product_control_email_deliveries
 ADD COLUMN unsubscribe_hash bytea UNIQUE CHECK(unsubscribe_hash IS NULL OR octet_length(unsubscribe_hash)=32),
 ADD COLUMN unsubscribe_sealed bytea NOT NULL DEFAULT '' CHECK(octet_length(unsubscribe_sealed)<=4096),
 ADD CONSTRAINT product_email_unsubscribe_shape CHECK((unsubscribe_hash IS NULL)=(octet_length(unsubscribe_sealed)=0)),
 DROP CONSTRAINT product_control_email_deliveries_reason_check,
 ADD CONSTRAINT product_control_email_deliveries_reason_check CHECK(reason IN ('','access_changed','preferences_changed','source_changed','authorization_unavailable','transport_unavailable','provider_rejected','retry_exhausted','expired','recipient_suppressed','unsubscribed'));

-- Each opt-out is backed by a retained delivery. Existing delivery caps and
-- pruning bound receipt storage without ever refusing an opt-out for capacity.
CREATE TABLE product_control_email_opt_outs (
 id uuid PRIMARY KEY CHECK(id<>'00000000-0000-0000-0000-000000000000'),
 subscription_id uuid NOT NULL REFERENCES product_control_email_subscriptions(id) ON DELETE CASCADE,
 delivery_id uuid NOT NULL REFERENCES product_control_email_deliveries(id) ON DELETE CASCADE,
 expected_version bigint NOT NULL CHECK(expected_version BETWEEN 1 AND 1000000),
 version bigint NOT NULL CHECK(version=LEAST(expected_version+1,1000000)),
 generation bigint NOT NULL CHECK(generation BETWEEN 1 AND 1000000),
 unsubscribed_at timestamptz NOT NULL CHECK(isfinite(unsubscribed_at)),
 UNIQUE(subscription_id,expected_version)
);
CREATE INDEX product_email_opt_out_recent ON product_control_email_opt_outs(subscription_id,unsubscribed_at DESC,id DESC);
