-- +goose Up
-- Older versions cached the one-time plaintext consumer key response when
-- callers sent Idempotency-Key. Remove those responses on upgrade. The
-- endpoint no longer uses the generic response cache.
delete from idempotency_keys
where position(convert_to('"key":"ck_', 'UTF8') in response_body) > 0;

-- +goose Down
-- Deleted plaintext responses must never be restored.
