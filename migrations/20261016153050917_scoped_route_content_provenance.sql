-- filename: 20261016153050917_scoped_route_content_provenance.sql

-- Deployment-isolated learned cacheability (ADR-1345).
-- +goose Up
ALTER TABLE request_telemetry DROP CONSTRAINT request_telemetry_content_outcome_check;
ALTER TABLE request_telemetry ADD CONSTRAINT request_telemetry_content_outcome_check CHECK (content_outcome IN ('', 'uncacheable', 'first', 'same_60', 'same_300', 'same_3600', 'same_long', 'changed_60', 'changed_300', 'changed_3600', 'changed_long', 'deployment_v1:uncacheable', 'deployment_v1:first', 'deployment_v1:same_60', 'deployment_v1:same_300', 'deployment_v1:same_3600', 'deployment_v1:same_long', 'deployment_v1:changed_60', 'deployment_v1:changed_300', 'deployment_v1:changed_3600', 'deployment_v1:changed_long'));

-- +goose Down
-- Versioned observations cannot be relabelled as legacy comparisons.
UPDATE request_telemetry SET content_outcome='' WHERE content_outcome LIKE 'deployment_v1:%';
ALTER TABLE request_telemetry DROP CONSTRAINT request_telemetry_content_outcome_check;
ALTER TABLE request_telemetry ADD CONSTRAINT request_telemetry_content_outcome_check CHECK (content_outcome IN ('', 'uncacheable', 'first', 'same_60', 'same_300', 'same_3600', 'same_long', 'changed_60', 'changed_300', 'changed_3600', 'changed_long'));
