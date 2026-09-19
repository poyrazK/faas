-- filename: 20260920020012345_security_scan_quarantine.sql
-- +goose Up
-- +goose StatementBegin

-- Scheduled image re-scans quarantine live enforce-mode deployments by
-- stamping deployments.parked_reason before flipping the parent app to
-- evicted_cold. Keep the reason closed-set and durable so wake/admission
-- paths can fail closed even if the app_changed notification is missed.
ALTER TABLE deployments
  DROP CONSTRAINT IF EXISTS deployments_parked_reason_check;
ALTER TABLE deployments
  ADD CONSTRAINT deployments_parked_reason_check
  CHECK (parked_reason IS NULL OR parked_reason IN (
    'liveness_exhausted',
    'lifecycle_park',
    'admin_park',
    'security_scan_regressed'
  ));

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

ALTER TABLE IF EXISTS deployments
  DROP CONSTRAINT IF EXISTS deployments_parked_reason_check;
ALTER TABLE IF EXISTS deployments
  ADD CONSTRAINT deployments_parked_reason_check
  CHECK (parked_reason IS NULL OR parked_reason IN (
    'liveness_exhausted',
    'lifecycle_park',
    'admin_park'
  ));

-- +goose StatementEnd
