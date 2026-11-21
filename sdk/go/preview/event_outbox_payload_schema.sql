-- Application-owned upgrade: permit payload erasure after confirmed publication.
-- Apply explicitly inside the application's migration transaction.
ALTER TABLE public.gregale_event_outbox DROP CONSTRAINT IF EXISTS gregale_event_outbox_request_body_check;
ALTER TABLE public.gregale_event_outbox ADD CONSTRAINT gregale_event_outbox_request_body_check
 CHECK(request_body IS NULL OR (octet_length(request_body) BETWEEN 1 AND 1048576 AND request_body::jsonb IS NOT NULL));
