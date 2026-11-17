-- Application-owned, payload-free Stripe handoff receipts. Apply explicitly in a migration.
CREATE TABLE IF NOT EXISTS public.gregale_stripe_webhook_receipts (
 account_id uuid NOT NULL,
 app_id uuid NOT NULL,
 endpoint_id uuid NOT NULL,
 event_id text NOT NULL CHECK(event_id ~ '^evt_[A-Za-z0-9_-]+$' AND octet_length(event_id)<=256),
 identity text NOT NULL CHECK(identity ~ '^[0-9a-f]{64}$'),
 binding_digest bytea NOT NULL CHECK(octet_length(binding_digest)=32),
 body_digest bytea NOT NULL CHECK(octet_length(body_digest)=32),
 state text NOT NULL DEFAULT 'processing' CHECK(state IN ('processing','staged','ignored')),
 owner_id text CHECK(owner_id ~ '^[A-Za-z0-9_.:-]{1,128}$'),
 outbox_key text CHECK(outbox_key ~ '^stripe-webhook:[0-9a-f]{64}$'),
 reason text NOT NULL DEFAULT '' CHECK(reason IN ('','not_applicable','already_applied','cancelled','superseded')),
 created_at timestamptz NOT NULL DEFAULT clock_timestamp() CHECK(isfinite(created_at)),
 updated_at timestamptz NOT NULL DEFAULT clock_timestamp() CHECK(isfinite(updated_at)),
 PRIMARY KEY(account_id,app_id,endpoint_id,event_id),
 CHECK((state='processing')=(owner_id IS NULL)),
 CHECK((state='staged')=(outbox_key IS NOT NULL)),
 CHECK((state='ignored')=(reason<>''))
);
