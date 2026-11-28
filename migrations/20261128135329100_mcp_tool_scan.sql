-- filename: 20261128135329100_mcp_tool_scan.sql
-- migration-compat: expand

-- +goose Up
-- +goose StatementBegin
SET LOCAL lock_timeout = '5s';
-- ADR-20261128135329: the tool safety scan for sold MCP servers.
--
-- A listing keeps the scan of the catalog it was published with (the
-- mcptoolscan.Report JSON), its high-severity count, and the seller's review
-- reason when it was published despite high findings.
ALTER TABLE mcp_listings
    ADD COLUMN IF NOT EXISTS tool_scan jsonb,
    ADD COLUMN IF NOT EXISTS tool_scan_at timestamptz,
    ADD COLUMN IF NOT EXISTS tool_scan_high integer NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS tool_scan_review text NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS tool_scan_reviewed_by text NOT NULL DEFAULT '';
-- An override admits either a contract refusal (breaking or unverified) or
-- a tool scan refusal, never both.
ALTER TABLE mcp_contract_overrides
    ADD COLUMN IF NOT EXISTS kind text NOT NULL DEFAULT 'contract';
-- A check keeps the candidate's scan, its high count, how many of those the
-- effective baseline had not already reviewed, the override that admitted
-- them, and whether its high findings were admitted (the review that
-- carries forward through the baseline).
ALTER TABLE mcp_contract_checks
    ADD COLUMN IF NOT EXISTS tool_scan jsonb,
    ADD COLUMN IF NOT EXISTS tool_scan_high integer NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS tool_scan_new_high integer NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS tool_scan_override_id uuid REFERENCES mcp_contract_overrides(id) ON DELETE SET NULL,
    ADD COLUMN IF NOT EXISTS tool_scan_reviewed boolean NOT NULL DEFAULT false;
-- Guarded so a replay over an already-applied schema is a no-op.
DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'mcp_listings_tool_scan_chk' AND conrelid = 'mcp_listings'::regclass) THEN
        ALTER TABLE mcp_listings ADD CONSTRAINT mcp_listings_tool_scan_chk CHECK (
            (tool_scan IS NULL) = (tool_scan_at IS NULL)
            AND (tool_scan IS NULL OR (jsonb_typeof(tool_scan) = 'object' AND octet_length(tool_scan::text) <= 262144))
            AND tool_scan_high >= 0
            AND octet_length(tool_scan_review) <= 512
            AND char_length(tool_scan_reviewed_by) <= 200
            AND (tool_scan_review = '') = (tool_scan_reviewed_by = '')) NOT VALID;
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'mcp_contract_overrides_kind_chk' AND conrelid = 'mcp_contract_overrides'::regclass) THEN
        ALTER TABLE mcp_contract_overrides ADD CONSTRAINT mcp_contract_overrides_kind_chk
            CHECK (kind IN ('contract', 'tool_scan')) NOT VALID;
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'mcp_contract_checks_tool_scan_chk' AND conrelid = 'mcp_contract_checks'::regclass) THEN
        ALTER TABLE mcp_contract_checks ADD CONSTRAINT mcp_contract_checks_tool_scan_chk CHECK (
            (tool_scan IS NULL OR (jsonb_typeof(tool_scan) = 'object' AND octet_length(tool_scan::text) <= 262144))
            AND tool_scan_high >= 0 AND tool_scan_new_high BETWEEN 0 AND tool_scan_high
            AND (tool_scan IS NOT NULL OR tool_scan_high = 0)
            AND (NOT tool_scan_reviewed OR tool_scan_high > 0)) NOT VALID;
    END IF;
END $$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE mcp_contract_checks DROP CONSTRAINT IF EXISTS mcp_contract_checks_tool_scan_chk;
ALTER TABLE mcp_contract_overrides DROP CONSTRAINT IF EXISTS mcp_contract_overrides_kind_chk;
ALTER TABLE mcp_listings DROP CONSTRAINT IF EXISTS mcp_listings_tool_scan_chk;
ALTER TABLE mcp_contract_checks
    DROP COLUMN IF EXISTS tool_scan_reviewed,
    DROP COLUMN IF EXISTS tool_scan_override_id,
    DROP COLUMN IF EXISTS tool_scan_new_high,
    DROP COLUMN IF EXISTS tool_scan_high,
    DROP COLUMN IF EXISTS tool_scan;
ALTER TABLE mcp_contract_overrides DROP COLUMN IF EXISTS kind;
ALTER TABLE mcp_listings
    DROP COLUMN IF EXISTS tool_scan_reviewed_by,
    DROP COLUMN IF EXISTS tool_scan_review,
    DROP COLUMN IF EXISTS tool_scan_high,
    DROP COLUMN IF EXISTS tool_scan_at,
    DROP COLUMN IF EXISTS tool_scan;
-- +goose StatementEnd
