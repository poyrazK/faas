-- Existing application outboxes only: apply both statements atomically in the
-- application's migration transaction. Fresh EventOutboxSchema includes this.
ALTER TABLE public.gregale_event_outbox
 DROP CONSTRAINT gregale_event_outbox_last_code_check;
ALTER TABLE public.gregale_event_outbox
 ADD CONSTRAINT gregale_event_outbox_last_code_check
 CHECK(last_code IN ('','unconfirmed','publication_confirmed','publication_rejected','retry_window_expired','attempts_exhausted'));
