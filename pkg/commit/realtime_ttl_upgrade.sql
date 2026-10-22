-- Explicit customer-owner upgrade; the platform never executes customer DDL.
ALTER TABLE public.gregale_realtime_outbox ADD COLUMN ttl_seconds integer NOT NULL
 DEFAULT coalesce(nullif(current_setting('gregale.realtime_ttl_seconds', true), '')::integer, 0)
 CHECK (ttl_seconds BETWEEN 0 AND 86400);
