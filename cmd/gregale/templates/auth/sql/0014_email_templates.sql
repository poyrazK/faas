-- The owner's wording for the emails the Auth app sends (ADR-1190). A row
-- replaces the default subject and body of one email type; deleting it
-- restores the default. emails.mjs validates placeholders and renders.
CREATE TABLE gregale_auth.email_templates (
    type text PRIMARY KEY CHECK (type IN ('signup', 'invite', 'magiclink', 'recovery', 'email_change', 'code', 'security_notice', 'organization_invitation')),
    subject text NOT NULL CHECK (char_length(subject) BETWEEN 1 AND 150),
    body text NOT NULL CHECK (octet_length(body) BETWEEN 1 AND 4000),
    updated_at timestamptz NOT NULL DEFAULT now()
);
