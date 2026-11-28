-- Security events (ADR-1190). What the front's sign-in protection did, for
-- the owner to read on the dashboard's Activity tab and with `gregale auth
-- activity --security`: wrong passwords and codes, holds, passes past the
-- account budget (a CAPTCHA or a trusted network), message limits and
-- new-device alerts. GoTrue's audit log records none of these.
--
-- A row stands for one or more alike events: wrong passwords from one
-- network for one account in one minute, or every attempt refused by one
-- spent budget until it reopens, share a row whose count grows (dedupe is
-- the digest of what makes them alike), so an attack grows the count, not
-- the table. Where a request came from is kept as its network (an IPv4 /24
-- or IPv6 /48) and a keyed digest of the address, which groups requests from
-- one address without storing it. budget_key names the attempt budget a hold
-- spent, so the owner sees whether it still holds. Rows go
-- AUTH_SECURITY_LOG_DAYS (30 by default) after their last event, and with their user. Only the
-- auth owner reads or writes the table.
SET LOCAL lock_timeout = '5s';

CREATE TABLE gregale_auth.security_events (
  id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  kind text NOT NULL CHECK (kind IN ('password_failed', 'code_failed', 'held', 'captcha_pass', 'trusted_pass', 'message_limited', 'new_device_alert')),
  user_id uuid REFERENCES auth.users (id) ON DELETE CASCADE,
  network text CHECK (network IS NULL OR length(network) <= 64),
  address_hash bytea CHECK (address_hash IS NULL OR octet_length(address_hash) = 16),
  detail jsonb NOT NULL DEFAULT '{}' CHECK (jsonb_typeof(detail) = 'object' AND octet_length(detail::text) <= 1024),
  count integer NOT NULL DEFAULT 1 CHECK (count > 0),
  dedupe bytea UNIQUE CHECK (dedupe IS NULL OR octet_length(dedupe) = 32),
  budget_key bytea CHECK (budget_key IS NULL OR octet_length(budget_key) = 32),
  first_at timestamptz NOT NULL DEFAULT now(),
  last_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX security_events_last_idx ON gregale_auth.security_events (last_at DESC, id DESC);
CREATE INDEX security_events_user_idx ON gregale_auth.security_events (user_id, last_at DESC) WHERE user_id IS NOT NULL;
REVOKE ALL ON gregale_auth.security_events FROM PUBLIC;
