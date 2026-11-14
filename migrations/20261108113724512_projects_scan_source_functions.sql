-- filename: 20261108113724512_projects_scan_source_functions.sql
-- migration-compat: expand
-- A functions project (gregale.yaml functions:) is a declared project source
-- (ADR-20261108160000), so projects.scan_source admits 'functions'. The CHECK
-- is replaced in three steps, each its own statement: the wider constraint is
-- added NOT VALID under a new name, validated (SHARE UPDATE EXCLUSIVE, so
-- writes continue), then the old one is dropped. A release that never writes
-- 'functions' runs unchanged beside this schema. Every step is re-runnable.
-- +goose NO TRANSACTION

-- +goose Up
SET lock_timeout = '5s';

-- +goose StatementBegin
DO $scan_source_functions$
BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'projects'::regclass AND conname = 'projects_scan_source_functions_chk') THEN
    ALTER TABLE projects ADD CONSTRAINT projects_scan_source_functions_chk CHECK (scan_source IN
      ('compose','procfile','k8s','render','fly','serverless',
       'workspace','convention','single','unknown','functions')) NOT VALID;
  END IF;
END
$scan_source_functions$;
-- +goose StatementEnd

ALTER TABLE projects VALIDATE CONSTRAINT projects_scan_source_functions_chk;

ALTER TABLE projects DROP CONSTRAINT IF EXISTS projects_scan_source_chk;

-- +goose Down
SET lock_timeout = '5s';

-- +goose StatementBegin
DO $scan_source_functions_down$
BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'projects'::regclass AND conname = 'projects_scan_source_chk') THEN
    ALTER TABLE projects ADD CONSTRAINT projects_scan_source_chk CHECK (scan_source IN
      ('compose','procfile','k8s','render','fly','serverless',
       'workspace','convention','single','unknown')) NOT VALID;
  END IF;
END
$scan_source_functions_down$;
-- +goose StatementEnd

ALTER TABLE projects DROP CONSTRAINT IF EXISTS projects_scan_source_functions_chk;
