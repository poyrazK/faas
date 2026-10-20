-- Who may sign up while sign-up is by invitation or waitlist (ADR-1190).
-- An address may sign up when it is approved here or holds a pending
-- organization invitation. With the waitlist, a refused sign-up records a
-- request that the app's operator approves; approving also lets addresses
-- in that never asked. The front checks this in GoTrue's before-user-created
-- hook, which GoTrue calls outside its own transactions.
SET LOCAL lock_timeout = '5s';

CREATE TABLE gregale_auth.signup_access (
    email text PRIMARY KEY CHECK (char_length(email) BETWEEN 3 AND 320 AND email = lower(email)),
    requested_at timestamptz,
    approved_at timestamptz,
    notified_at timestamptz,
    CHECK (requested_at IS NOT NULL OR approved_at IS NOT NULL)
);
CREATE INDEX signup_access_pending_idx ON gregale_auth.signup_access (requested_at) WHERE approved_at IS NULL;

-- The email that tells a waiting person they may sign up now.
ALTER TABLE gregale_auth.email_templates DROP CONSTRAINT email_templates_type_check,
    ADD CONSTRAINT email_templates_type_check CHECK (type IN ('signup', 'invite', 'magiclink', 'recovery', 'email_change', 'code', 'security_notice', 'organization_invitation', 'signup_approved'));
