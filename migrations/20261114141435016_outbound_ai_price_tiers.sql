-- filename: 20261114141435016_outbound_ai_price_tiers.sql
-- ADR 20261027132936 amendment 3: a customer price list entry can set the
-- price of one-hour cache writes and a long-context tier (the prompt size
-- it starts above and the percentages it scales input-side and output
-- prices by). All four columns are nullable; the tier's three are set
-- together or not at all.
--
-- The CHECK is added NOT VALID and validated in its own statement, so the
-- write-blocking lock is not held while existing rows are scanned. Every
-- step is guarded, so a rerun after a partial failure converges.
-- migration-compat: expand
-- +goose NO TRANSACTION

-- +goose Up
SET lock_timeout = '5s';
ALTER TABLE outbound_ai_price_entries
    ADD COLUMN IF NOT EXISTS cache_write_1h_millicents_per_mtok bigint,
    ADD COLUMN IF NOT EXISTS long_context_above_input_tokens bigint,
    ADD COLUMN IF NOT EXISTS long_context_input_percent integer,
    ADD COLUMN IF NOT EXISTS long_context_output_percent integer;
-- +goose StatementBegin
DO $$ BEGIN
 IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'outbound_ai_price_entries'::regclass
                AND conname = 'outbound_ai_price_entries_tiers_chk') THEN
  ALTER TABLE outbound_ai_price_entries ADD CONSTRAINT outbound_ai_price_entries_tiers_chk
    CHECK ((cache_write_1h_millicents_per_mtok IS NULL OR cache_write_1h_millicents_per_mtok BETWEEN 0 AND 1000000000)
           AND ((long_context_above_input_tokens IS NULL AND long_context_input_percent IS NULL AND long_context_output_percent IS NULL)
                OR (long_context_above_input_tokens BETWEEN 1000 AND 2000000
                    AND long_context_input_percent BETWEEN 100 AND 1000
                    AND long_context_output_percent BETWEEN 100 AND 1000))) NOT VALID;
 END IF;
END $$;
-- +goose StatementEnd
ALTER TABLE outbound_ai_price_entries VALIDATE CONSTRAINT outbound_ai_price_entries_tiers_chk;
RESET lock_timeout;

-- +goose Down
SET lock_timeout = '5s';
ALTER TABLE outbound_ai_price_entries
    DROP CONSTRAINT IF EXISTS outbound_ai_price_entries_tiers_chk,
    DROP COLUMN IF EXISTS long_context_output_percent,
    DROP COLUMN IF EXISTS long_context_input_percent,
    DROP COLUMN IF EXISTS long_context_above_input_tokens,
    DROP COLUMN IF EXISTS cache_write_1h_millicents_per_mtok;
RESET lock_timeout;
