-- +goose Up

CREATE TABLE IF NOT EXISTS event_acknowledgment_outcomes (
 invocation_id uuid NOT NULL REFERENCES invocations(id) ON DELETE CASCADE,
 attempt integer NOT NULL CHECK (attempt > 0),
 replay_generation bigint NOT NULL CHECK (replay_generation >= 0),
 kind text NOT NULL CHECK (kind IN ('accepted','renewed','renewal_rejected','completed','expired')),
 first_at timestamptz NOT NULL,
 last_at timestamptz NOT NULL CHECK (last_at >= first_at),
 occurrences bigint NOT NULL CHECK (occurrences BETWEEN 1 AND 2147483647),
 retain_until timestamptz NOT NULL,
 PRIMARY KEY (invocation_id,attempt,replay_generation,kind)
);
CREATE INDEX IF NOT EXISTS event_acknowledgment_outcomes_retention ON event_acknowledgment_outcomes(retain_until);

-- +goose Down
SELECT 1;
