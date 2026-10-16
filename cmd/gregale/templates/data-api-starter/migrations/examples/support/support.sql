-- Copy into a new numbered migration; adapt the example binding role deliberately.
CREATE SCHEMA support_private;
REVOKE ALL ON SCHEMA support_private FROM PUBLIC;
CREATE TABLE support_private.cases (
 id integer GENERATED ALWAYS AS IDENTITY,
 tenant text NOT NULL,
 title text NOT NULL,
 status text NOT NULL CHECK(status IN ('open','closed')),
 internal_notes text NOT NULL DEFAULT '',
 PRIMARY KEY (tenant,id)
);
CREATE TABLE support_private.replies (
 id integer GENERATED ALWAYS AS IDENTITY,
 tenant text NOT NULL,
 case_id integer NOT NULL,
 body text NOT NULL,
 internal_notes text NOT NULL DEFAULT '',
 PRIMARY KEY (tenant,id),
 FOREIGN KEY (tenant,case_id) REFERENCES support_private.cases(tenant,id)
);
ALTER TABLE support_private.cases ENABLE ROW LEVEL SECURITY;
ALTER TABLE support_private.cases FORCE ROW LEVEL SECURITY;
ALTER TABLE support_private.replies ENABLE ROW LEVEL SECURITY;
ALTER TABLE support_private.replies FORCE ROW LEVEL SECURITY;
CREATE POLICY support_cases_read ON support_private.cases FOR SELECT
 USING (tenant = (current_setting('request.jwt.claims',true)::jsonb ->> 'tenant')
 AND current_setting('request.jwt.claims',true)::jsonb ->> 'gregale_read_only' = 'true');
CREATE POLICY support_replies_read ON support_private.replies FOR SELECT
 USING (tenant = (current_setting('request.jwt.claims',true)::jsonb ->> 'tenant')
 AND current_setting('request.jwt.claims',true)::jsonb ->> 'gregale_read_only' = 'true');
REVOKE ALL ON support_private.cases,support_private.replies FROM PUBLIC,gregale_data_api;
GRANT USAGE ON SCHEMA support_private TO gregale_data_api;
GRANT SELECT (id,tenant,title,status) ON support_private.cases TO gregale_data_api;
GRANT SELECT (id,tenant,case_id,body) ON support_private.replies TO gregale_data_api;
CREATE VIEW api.support_cases WITH (security_invoker=true,security_barrier=true)
 AS SELECT id,tenant,title,status FROM support_private.cases;
CREATE VIEW api.support_replies WITH (security_invoker=true,security_barrier=true)
 AS SELECT id,tenant,case_id,body FROM support_private.replies;
GRANT SELECT ON api.support_cases,api.support_replies TO gregale_data_api;
