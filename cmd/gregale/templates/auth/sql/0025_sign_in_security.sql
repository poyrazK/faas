-- Sign-in networks and devices (ADR-1190). The front records, for each
-- user, the client networks (an IPv4 address or IPv6 /64) and device kinds
-- (a browser or app's user agent without version numbers) that received
-- tokens. A network seen in the last 30 days lets its user past the
-- account-wide failed-password budget, so a stranger who spends that budget
-- cannot lock the owner out; a sign-in from both a network and a device not
-- seen in 90 days sends a new-device email. As in attempt_budgets, keys are
-- SHA-256 digests of the kind, the user ID and the value, so no address or
-- user agent is stored. Only the auth owner reads or writes the table.
SET LOCAL lock_timeout = '5s';

CREATE TABLE gregale_auth.sign_in_seen (
  key bytea PRIMARY KEY CHECK (octet_length(key) = 32),
  user_id uuid NOT NULL REFERENCES auth.users (id) ON DELETE CASCADE,
  last_seen timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX sign_in_seen_user_idx ON gregale_auth.sign_in_seen (user_id);
CREATE INDEX sign_in_seen_last_seen_idx ON gregale_auth.sign_in_seen (last_seen);
REVOKE ALL ON gregale_auth.sign_in_seen FROM PUBLIC;

-- The email that tells a person their account signed in from a new device.
ALTER TABLE gregale_auth.email_templates DROP CONSTRAINT email_templates_type_check,
    ADD CONSTRAINT email_templates_type_check CHECK (type IN ('signup', 'invite', 'magiclink', 'recovery', 'email_change', 'code', 'security_notice', 'organization_invitation', 'signup_approved', 'new_device'));
