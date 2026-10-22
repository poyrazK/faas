BEGIN;
-- Execute explicitly as the customer database owner before binding a version 3 source.
ALTER TABLE public.gregale_outbox ADD COLUMN IF NOT EXISTS schema_version text NOT NULL DEFAULT '';
ALTER TABLE public.gregale_outbox ADD COLUMN IF NOT EXISTS publication_acceptance_id bigint CHECK (publication_acceptance_id>0);
ALTER TABLE public.gregale_outbox DROP CONSTRAINT IF EXISTS gregale_outbox_delivery_identity;
ALTER TABLE public.gregale_outbox ADD CONSTRAINT gregale_outbox_delivery_identity CHECK (
 (accepted_at IS NULL AND receipt_id IS NULL AND invocation_id IS NULL AND operation_id IS NULL AND publication_acceptance_id IS NULL)
 OR (accepted_at IS NOT NULL AND receipt_id IS NOT NULL AND
 (invocation_id IS NOT NULL)::integer + (operation_id IS NOT NULL)::integer + (publication_acceptance_id IS NOT NULL)::integer=1));

COMMIT;
