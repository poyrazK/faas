-- Impersonation (ADR-1190): the app's operator, holding an admin key, opens
-- a time-limited session as a user to see what they see. Each one is kept
-- here with the key that opened it and the reason given, after it ends too;
-- the access token hook marks the session's tokens with act (RFC 8693).
SET LOCAL lock_timeout = '5s';

CREATE TABLE gregale_auth.impersonations (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id uuid NOT NULL REFERENCES auth.users (id) ON DELETE CASCADE,
    session_id uuid NOT NULL,
    actor text NOT NULL CHECK (actor ~ '^admin:[0-9A-F]{8}$'),
    reason text NOT NULL CHECK (char_length(reason) BETWEEN 1 AND 200),
    started_at timestamptz NOT NULL DEFAULT now(),
    expires_at timestamptz NOT NULL,
    ended_at timestamptz,
    CHECK (expires_at > started_at)
);
CREATE INDEX impersonations_session_idx ON gregale_auth.impersonations (session_id);
CREATE INDEX impersonations_user_idx ON gregale_auth.impersonations (user_id, started_at DESC);
