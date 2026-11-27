-- Failed-attempt budgets (ADR-1190). GoTrue's own rate limits count per
-- client address in each instance's memory: a restored snapshot starts them
-- at zero, instances do not share them, and none counts per account. The
-- front records failed password sign-ins, second-step codes and
-- service-account secrets here instead, so every instance and every wake
-- sees the same counts. Keys are SHA-256 digests of the budget's name and
-- subject (an address, a user ID, a client address), so no address or
-- number is stored. Only the auth owner reads or writes the table.
SET LOCAL lock_timeout = '5s';

CREATE TABLE gregale_auth.attempt_budgets (
  key bytea PRIMARY KEY CHECK (octet_length(key) = 32),
  failures integer NOT NULL CHECK (failures > 0),
  window_ends_at timestamptz NOT NULL
);
CREATE INDEX attempt_budgets_window_idx ON gregale_auth.attempt_budgets (window_ends_at);
REVOKE ALL ON gregale_auth.attempt_budgets FROM PUBLIC;
