-- +goose Up
-- Repair restored databases whose migration ledger includes 00440 but whose
-- request_telemetry table lost the aggregate count column. Preserve existing
-- rows as single requests; healthy databases keep their current counts.
ALTER TABLE request_telemetry
    ADD COLUMN IF NOT EXISTS count int NOT NULL DEFAULT 1 CHECK (count >= 1);

-- +goose Down
-- Forward-only: dropping count would lose collapsed request totals.
SELECT 1;
