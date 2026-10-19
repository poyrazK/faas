-- migration-compat: expand
-- filename: 20261016170649001_cache_rule_refills.sql

-- ADR-1199: cache rules checked from refill evidence. Request telemetry gains
-- the stale content outcome: on a route with a cache rule, the stored body
-- changed within the rule's max-age. The check is re-added NOT VALID, so this
-- migration holds its exclusive lock only briefly; 20261019072506758
-- validates it in a separate transaction under a lock that allows writes.

-- +goose Up
-- +goose StatementBegin
SET LOCAL lock_timeout = '5s';
ALTER TABLE request_telemetry DROP CONSTRAINT IF EXISTS request_telemetry_content_outcome_check;
ALTER TABLE request_telemetry
  ADD CONSTRAINT request_telemetry_content_outcome_check CHECK (content_outcome IN (
    '', 'uncacheable', 'first',
    'same_60', 'same_300', 'same_3600', 'same_long',
    'changed_60', 'changed_300', 'changed_3600', 'changed_long',
    'stale'
  )) NOT VALID;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
SET LOCAL lock_timeout = '5s';
ALTER TABLE request_telemetry DROP CONSTRAINT IF EXISTS request_telemetry_content_outcome_check;
ALTER TABLE request_telemetry
  ADD CONSTRAINT request_telemetry_content_outcome_check CHECK (content_outcome IN (
    '', 'uncacheable', 'first',
    'same_60', 'same_300', 'same_3600', 'same_long',
    'changed_60', 'changed_300', 'changed_3600', 'changed_long'
  )) NOT VALID;
-- +goose StatementEnd
