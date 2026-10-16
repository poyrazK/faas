-- Apply as database owner; grant the dedicated runtime login access separately.
CREATE SCHEMA support_audit;
REVOKE ALL ON SCHEMA support_audit FROM PUBLIC;
CREATE TABLE support_audit.events (
 id uuid PRIMARY KEY,
 time timestamptz NOT NULL,
 subject text NOT NULL CHECK(length(subject) BETWEEN 1 AND 256),
 tenant text NOT NULL CHECK(length(tenant) BETWEEN 1 AND 256),
 action text NOT NULL CHECK(length(action) BETWEEN 1 AND 100),
 resource text CHECK(length(resource)<=100),
 outcome text NOT NULL CHECK(outcome IN ('success','failed','denied')),
 status integer NOT NULL CHECK(status BETWEEN 100 AND 599)
);
CREATE INDEX ON support_audit.events(tenant,time DESC,id DESC);
ALTER TABLE support_audit.events ENABLE ROW LEVEL SECURITY;
ALTER TABLE support_audit.events FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_access ON support_audit.events
 USING (tenant=current_setting('support_audit.tenant',true))
 WITH CHECK (tenant=current_setting('support_audit.tenant',true));
REVOKE ALL ON support_audit.events FROM PUBLIC;
-- GRANT USAGE ON SCHEMA support_audit TO your_dedicated_audit_login;
-- GRANT SELECT,INSERT,DELETE ON support_audit.events TO your_dedicated_audit_login;
-- Do not grant UPDATE, ownership, superuser, or BYPASSRLS.
-- Schedule owner-side cleanup for inactive tenants:
-- DELETE FROM support_audit.events WHERE time < now()-interval '7 days';
