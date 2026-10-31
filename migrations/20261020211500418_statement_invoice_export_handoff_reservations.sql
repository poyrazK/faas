-- filename: 20261020211500418_statement_invoice_export_handoff_reservations.sql

-- ADR-20261020211500: an invoice export reserves its statement's handoff
-- before it calls the provider, so ADR-238's overlap rules stop an
-- overlapping statement from being invoiced while the export is in flight.
-- A refused attempt releases the reservation.

-- migration-compat: expand

-- +goose Up
SET LOCAL lock_timeout = '5s';
ALTER TABLE usage_statement_invoice_exports
  ADD COLUMN IF NOT EXISTS handoff_reserved boolean NOT NULL DEFAULT false;

-- +goose Down
ALTER TABLE usage_statement_invoice_exports
  DROP COLUMN IF EXISTS handoff_reserved;
