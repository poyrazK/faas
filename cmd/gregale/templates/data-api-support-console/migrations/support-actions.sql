-- Apply once after the support example; keep this schema outside the Data API.
ALTER TABLE support_private.cases ADD COLUMN version integer NOT NULL DEFAULT 1;
CREATE OR REPLACE VIEW api.support_cases WITH (security_invoker=true,security_barrier=true)
 AS SELECT id,tenant,title,status,version FROM support_private.cases;
-- Grant SELECT(version) on support_private.cases to the existing read binding login.
CREATE SCHEMA support_actions;
REVOKE ALL ON SCHEMA support_actions FROM PUBLIC;
CREATE TABLE support_actions.receipts (
 id uuid PRIMARY KEY, tenant text NOT NULL, subject text NOT NULL,
 action text NOT NULL, request_hash text NOT NULL, result jsonb NOT NULL,
 time timestamptz NOT NULL DEFAULT now()
);
REVOKE ALL ON support_actions.receipts FROM PUBLIC;
-- Function owner must be trusted, with private-table access; callers get EXECUTE only.
CREATE POLICY support_action_cases ON support_private.cases
 USING (tenant=current_setting('request.jwt.claims',true)::jsonb->>'tenant'
 AND current_setting('request.jwt.claims',true)::jsonb->'gregale_support_actions' ?| ARRAY['add_reply','close_case'])
 WITH CHECK (tenant=current_setting('request.jwt.claims',true)::jsonb->>'tenant'
 AND current_setting('request.jwt.claims',true)::jsonb->'gregale_support_actions' ?| ARRAY['add_reply','close_case']);
CREATE POLICY support_action_replies ON support_private.replies FOR INSERT
 WITH CHECK (tenant=current_setting('request.jwt.claims',true)::jsonb->>'tenant'
 AND current_setting('request.jwt.claims',true)::jsonb->'gregale_support_actions' ? 'add_reply');
CREATE FUNCTION support_actions.execute(p_action text,p_case integer,p_version integer,p_body text,p_id uuid,p_deadline timestamptz)
 RETURNS jsonb LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog AS $$
DECLARE
 claims jsonb := current_setting('request.jwt.claims',true)::jsonb;
 actor text := claims->>'sub'; account text := claims->>'tenant';
 prior support_actions.receipts; current_case support_private.cases;
 fingerprint text; answer jsonb; reply_id integer;
BEGIN
 IF actor IS NULL OR account IS NULL OR p_action NOT IN ('add_reply','close_case')
 OR NOT coalesce(claims->'gregale_support_actions' ? p_action,false)
 THEN RAISE EXCEPTION 'permission refused' USING ERRCODE='42501'; END IF;
 IF p_case IS NULL OR p_version IS NULL OR p_id IS NULL
 OR (p_action='add_reply' AND (p_body IS NULL OR length(btrim(p_body))=0 OR length(p_body)>4000))
 OR (p_action='close_case' AND p_body IS NOT NULL)
 THEN RAISE EXCEPTION 'invalid action input' USING ERRCODE='42501'; END IF;
 fingerprint := encode(sha256(convert_to(jsonb_build_array(p_action,p_case,p_version,p_body)::text,'UTF8')),'hex');
 -- Serialize each receipt, including concurrent retries, before checking it.
 PERFORM pg_advisory_xact_lock(hashtextextended(p_id::text,0));
 SELECT * INTO prior FROM support_actions.receipts WHERE id=p_id;
 IF FOUND THEN
  IF prior.tenant<>account OR prior.subject<>actor OR prior.action<>p_action OR prior.request_hash<>fingerprint
  THEN RAISE EXCEPTION 'receipt mismatch' USING ERRCODE='42501'; END IF;
  RETURN prior.result;
 END IF;
 IF p_deadline IS NULL OR p_deadline<clock_timestamp() THEN RAISE EXCEPTION 'confirmation expired' USING ERRCODE='40001'; END IF;
 SELECT * INTO current_case FROM support_private.cases WHERE tenant=account AND id=p_case FOR UPDATE;
 IF NOT FOUND OR current_case.version<>p_version OR current_case.status<>'open'
 THEN RAISE EXCEPTION 'case changed' USING ERRCODE='40001'; END IF;
 IF p_action='add_reply' THEN
  INSERT INTO support_private.replies(tenant,case_id,body) VALUES(account,p_case,p_body) RETURNING id INTO reply_id;
 ELSE
  UPDATE support_private.cases SET status='closed' WHERE tenant=account AND id=p_case;
 END IF;
 UPDATE support_private.cases SET version=version+1 WHERE tenant=account AND id=p_case;
 answer:=jsonb_build_object('action',p_action,'version',p_version+1,'status',CASE WHEN p_action='close_case' THEN 'closed' ELSE 'open' END);
 IF reply_id IS NOT NULL THEN answer:=answer||jsonb_build_object('reply_id',reply_id); END IF;
 -- Redacted actor/action/time/outcome and safe result commit with the mutation.
 INSERT INTO support_actions.receipts(id,tenant,subject,action,request_hash,result)
 VALUES(p_id,account,actor,p_action,fingerprint,answer);
 RETURN answer;
END $$;
REVOKE ALL ON FUNCTION support_actions.execute(text,integer,integer,text,uuid,timestamptz) FROM PUBLIC;
-- Dedicated console login:
-- GRANT USAGE ON SCHEMA support_actions TO your_action_login;
-- GRANT EXECUTE ON FUNCTION support_actions.execute(text,integer,integer,text,uuid,timestamptz) TO your_action_login;
-- Grant no private table access or receipt table access to this login.
