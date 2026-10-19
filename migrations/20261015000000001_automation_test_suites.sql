-- +goose Up
ALTER TABLE workflow_automation_definitions
 ADD COLUMN IF NOT EXISTS test_suite jsonb CONSTRAINT workflow_automation_test_suite_shape CHECK (test_suite IS NULL OR jsonb_typeof(test_suite)='object'),
 ADD COLUMN IF NOT EXISTS test_report jsonb CONSTRAINT workflow_automation_test_report_shape CHECK (test_report IS NULL OR jsonb_typeof(test_report)='object');

-- +goose Down
ALTER TABLE workflow_automation_definitions
 DROP COLUMN IF EXISTS test_report,
 DROP COLUMN IF EXISTS test_suite;
