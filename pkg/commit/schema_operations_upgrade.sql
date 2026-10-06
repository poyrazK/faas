-- Explicit owner action for the earlier internal invocation-only schema.
-- Accepted legacy identities remain intact. Unknown constraints are not removed.
BEGIN;
LOCK TABLE public.gregale_outbox IN ACCESS EXCLUSIVE MODE;
DO $$
DECLARE old_definition text;
BEGIN
 IF EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid='public.gregale_outbox'::regclass
 AND conname='gregale_outbox_delivery_identity') THEN
  RETURN;
 END IF;
 SELECT regexp_replace(pg_get_constraintdef(oid),'[[:space:]()]','','g')
 INTO old_definition FROM pg_constraint WHERE conrelid='public.gregale_outbox'::regclass
 AND conname='gregale_outbox_check' AND contype='c';
 IF old_definition IS DISTINCT FROM
 'CHECKaccepted_atISNULLANDreceipt_idISNULLANDinvocation_idISNULLORaccepted_atISNOTNULLANDreceipt_idISNOTNULLANDinvocation_idISNOTNULL' THEN
  RAISE EXCEPTION 'unknown Gregale outbox delivery constraint; owner review required';
 END IF;
 ALTER TABLE public.gregale_outbox ADD COLUMN operation_id uuid;
 ALTER TABLE public.gregale_outbox DROP CONSTRAINT gregale_outbox_check;
 ALTER TABLE public.gregale_outbox ADD CONSTRAINT gregale_outbox_delivery_identity CHECK (
  (accepted_at IS NULL AND receipt_id IS NULL AND invocation_id IS NULL AND operation_id IS NULL)
  OR (accepted_at IS NOT NULL AND receipt_id IS NOT NULL
  AND (invocation_id IS NOT NULL)::integer + (operation_id IS NOT NULL)::integer = 1));
END;
$$;
COMMIT;
