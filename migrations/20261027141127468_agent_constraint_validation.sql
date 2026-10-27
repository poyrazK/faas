-- filename: 20261027141127468_agent_constraint_validation.sql
-- ADR-20261027090010: validates the checks the agent migrations widened or
-- added NOT VALID on existing tables (app_webhooks, the two platform mail
-- outbox tables and triggers), in a later transaction, so each scan runs
-- without the ADD's lock blocking writes.
-- migration-compat: expand

-- +goose Up
SET LOCAL lock_timeout = '5s';
ALTER TABLE app_webhooks VALIDATE CONSTRAINT app_webhooks_scope_chk;
ALTER TABLE platform_mail_producer_outbox VALIDATE CONSTRAINT platform_mail_producer_outbox_template_check;
ALTER TABLE platform_mail_producer_outbox VALIDATE CONSTRAINT platform_mail_producer_outbox_verification_check;
ALTER TABLE platform_mail_producer_outbox VALIDATE CONSTRAINT platform_mail_producer_outbox_security_alert_check;
ALTER TABLE platform_mail_producer_outbox VALIDATE CONSTRAINT platform_mail_producer_outbox_agent_delivery_check;
ALTER TABLE platform_mail_outbox_reconciliations VALIDATE CONSTRAINT platform_mail_outbox_reconciliations_template_check;
ALTER TABLE triggers VALIDATE CONSTRAINT triggers_agent_check;

-- +goose Down
-- Validation leaves nothing to undo.
SELECT 1;
