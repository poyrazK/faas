-- filename: 20261024230605219_firecracker_cpu_template.sql
-- migration-compat: expand
-- Firecracker CPU templates and CPU-compatible snapshot restore
-- (ADR-20261024225612). vmmd records the CPU template it boots guests with
-- and its host CPU signature on its compute_nodes row; each snapshot records
-- the capturing node's pair so schedd restores it only on a compatible node.
-- The empty string identifies a row written before this change.
-- Expand-only: binaries without the ADR ignore the columns.

-- +goose Up
-- +goose StatementBegin
SET LOCAL lock_timeout = '5s';
ALTER TABLE compute_nodes
 ADD COLUMN IF NOT EXISTS cpu_template text NOT NULL DEFAULT '',
 ADD COLUMN IF NOT EXISTS cpu_model text NOT NULL DEFAULT '';
ALTER TABLE snapshots
 ADD COLUMN IF NOT EXISTS cpu_template text NOT NULL DEFAULT '',
 ADD COLUMN IF NOT EXISTS cpu_model text NOT NULL DEFAULT '';

COMMENT ON COLUMN compute_nodes.cpu_template IS
 'Firecracker CPU template vmmd boots guests with; empty for none (ADR-20261024225612).';
COMMENT ON COLUMN compute_nodes.cpu_model IS
 'Host CPU signature <class>/<vendor>-<family>-<model>-<stepping>/<flags hash>; empty before vmmd reports it.';
COMMENT ON COLUMN snapshots.cpu_template IS
 'CPU template of the capturing node; a templated snapshot restores on any node class the template supports.';
COMMENT ON COLUMN snapshots.cpu_model IS
 'Host CPU signature of the capturing node; a template-less snapshot restores only on the identical signature.';
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
-- Compatibility metadata is forward-only. Keeping the columns makes rollback
-- safe when rows were already written by newer binaries.
SELECT 1;
-- +goose StatementEnd
