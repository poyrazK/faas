-- filename: 20261016170649001_cache_rule_refills.sql

-- ADR-1199: cache rules checked from refill evidence. Request telemetry gains
-- the stale content outcome: on a route with a cache rule, the stored body
-- changed within the rule's max-age. Route insights gain the cache_stale and
-- cache_too_short kinds. Both checks are re-added NOT VALID and validated, so
-- the scan does not hold an exclusive lock.

-- +goose Up
-- +goose StatementBegin
ALTER TABLE request_telemetry DROP CONSTRAINT IF EXISTS request_telemetry_content_outcome_check;
ALTER TABLE request_telemetry
  ADD CONSTRAINT request_telemetry_content_outcome_check CHECK (content_outcome IN (
    '', 'uncacheable', 'first',
    'same_60', 'same_300', 'same_3600', 'same_long',
    'changed_60', 'changed_300', 'changed_3600', 'changed_long',
    'stale'
  )) NOT VALID;
ALTER TABLE request_telemetry VALIDATE CONSTRAINT request_telemetry_content_outcome_check;

ALTER TABLE route_insights DROP CONSTRAINT IF EXISTS route_insights_kind_check;
ALTER TABLE route_insights
  ADD CONSTRAINT route_insights_kind_check
  CHECK (kind IN ('kept_awake', 'cacheable', 'cache_stale', 'cache_too_short')) NOT VALID;
ALTER TABLE route_insights VALIDATE CONSTRAINT route_insights_kind_check;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DELETE FROM route_insights WHERE kind IN ('cache_stale', 'cache_too_short');
ALTER TABLE route_insights DROP CONSTRAINT IF EXISTS route_insights_kind_check;
ALTER TABLE route_insights
  ADD CONSTRAINT route_insights_kind_check CHECK (kind IN ('kept_awake', 'cacheable')) NOT VALID;

ALTER TABLE request_telemetry DROP CONSTRAINT IF EXISTS request_telemetry_content_outcome_check;
ALTER TABLE request_telemetry
  ADD CONSTRAINT request_telemetry_content_outcome_check CHECK (content_outcome IN (
    '', 'uncacheable', 'first',
    'same_60', 'same_300', 'same_3600', 'same_long',
    'changed_60', 'changed_300', 'changed_3600', 'changed_long'
  )) NOT VALID;
-- +goose StatementEnd
