-- Explicit owner upgrade; the relay never installs or modifies this schema.
BEGIN;
ALTER TABLE public.gregale_outbox ADD COLUMN IF NOT EXISTS routing jsonb;
DO $$ BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid='public.gregale_outbox'::regclass AND conname='gregale_outbox_routing_check') THEN
        ALTER TABLE public.gregale_outbox ADD CONSTRAINT gregale_outbox_routing_check CHECK (
            routing IS NULL OR COALESCE(jsonb_typeof(routing)='object'
            AND routing->'version'='2'::jsonb
            AND jsonb_typeof(routing->'key') IN ('string','number','boolean')
            AND (NOT routing ? 'platform_tenant_id' OR jsonb_typeof(routing->'platform_tenant_id')='string'),false));
    END IF;
END $$;

COMMIT;
