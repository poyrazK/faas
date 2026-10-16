-- Apply after case-priority.sql. Notes never enter customer-facing replies.
-- Preserve custom reply-view columns when adapting this example.
ALTER TABLE support_private.replies ADD COLUMN occurred_at timestamptz NOT NULL DEFAULT date_trunc('milliseconds',clock_timestamp());
CREATE OR REPLACE VIEW api.support_replies WITH(security_invoker=true,security_barrier=true)
 AS SELECT id,tenant,case_id,body,occurred_at FROM support_private.replies;
CREATE TABLE support_private.notes(
 id integer GENERATED ALWAYS AS IDENTITY PRIMARY KEY,tenant text NOT NULL,case_id integer NOT NULL,
 actor text NOT NULL,body text NOT NULL CHECK(length(btrim(body)) BETWEEN 1 AND 4000),
 occurred_at timestamptz NOT NULL DEFAULT date_trunc('milliseconds',clock_timestamp()),
 FOREIGN KEY(tenant,case_id) REFERENCES support_private.cases(tenant,id)
);
ALTER TABLE support_private.notes ENABLE ROW LEVEL SECURITY;
ALTER TABLE support_private.notes FORCE ROW LEVEL SECURITY;
CREATE POLICY notes_read ON support_private.notes FOR SELECT
 USING(tenant=current_setting('request.jwt.claims',true)::jsonb->>'tenant'
 AND current_setting('request.jwt.claims',true)::jsonb->>'gregale_read_only'='true'
 AND current_setting('request.jwt.claims',true)::jsonb->'gregale_internal_notes'='true'::jsonb);
CREATE POLICY notes_insert ON support_private.notes FOR INSERT
 WITH CHECK(tenant=current_setting('request.jwt.claims',true)::jsonb->>'tenant'
 AND current_setting('request.jwt.claims',true)::jsonb->'gregale_internal_notes'='true'::jsonb
 AND current_setting('request.jwt.claims',true)::jsonb->'gregale_support_actions' ? 'add_note');
CREATE POLICY notes_case_update ON support_private.cases
 USING(tenant=current_setting('request.jwt.claims',true)::jsonb->>'tenant'
 AND current_setting('request.jwt.claims',true)::jsonb->'gregale_internal_notes'='true'::jsonb
 AND current_setting('request.jwt.claims',true)::jsonb->'gregale_support_actions' ? 'add_note')
 WITH CHECK(tenant=current_setting('request.jwt.claims',true)::jsonb->>'tenant'
 AND current_setting('request.jwt.claims',true)::jsonb->'gregale_internal_notes'='true'::jsonb
 AND current_setting('request.jwt.claims',true)::jsonb->'gregale_support_actions' ? 'add_note');
REVOKE ALL ON support_private.notes FROM PUBLIC;
CREATE VIEW api.support_internal_notes WITH(security_invoker=true,security_barrier=true)
 AS SELECT id,tenant,case_id,actor,body,occurred_at FROM support_private.notes;
-- Grant approved SELECT columns and view SELECT only to the read binding role.
CREATE OR REPLACE FUNCTION support_actions.register(p_action text,p_case integer,p_version integer,p_body text,p_id uuid,p_deadline timestamptz)
 RETURNS void LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog AS $$
DECLARE c jsonb:=current_setting('request.jwt.claims',true)::jsonb; fingerprint text;
BEGIN
 IF c->>'sub' IS NULL OR c->>'tenant' IS NULL OR p_action NOT IN ('add_reply','close_case','assign_case','unassign_case','set_priority','add_note')
 OR NOT coalesce(c->'gregale_support_actions' ? p_action,false) OR p_deadline IS NULL
 THEN RAISE EXCEPTION 'permission refused' USING ERRCODE='42501'; END IF;
 fingerprint:=encode(sha256(convert_to(jsonb_build_array(p_action,p_case,p_version,p_body)::text,'UTF8')),'hex');
 INSERT INTO support_actions.requests(id,tenant,subject,action,request_hash,deadline,state)
 VALUES(p_id,c->>'tenant',c->>'sub',p_action,fingerprint,p_deadline,'previewed');
END $$;
CREATE OR REPLACE FUNCTION support_actions.execute_mutation(p_action text,p_case integer,p_version integer,p_body text,p_id uuid,p_deadline timestamptz)
 RETURNS jsonb LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog AS $$
DECLARE
 claims jsonb := current_setting('request.jwt.claims',true)::jsonb;
 actor text := claims->>'sub'; account text := claims->>'tenant';
 prior support_actions.receipts; current_case support_private.cases;
 fingerprint text; answer jsonb; reply_id integer;
BEGIN
 IF actor IS NULL OR account IS NULL OR p_action NOT IN ('add_reply','close_case','assign_case','unassign_case','set_priority','add_note')
 OR NOT coalesce(claims->'gregale_support_actions' ? p_action,false)
 THEN RAISE EXCEPTION 'permission refused' USING ERRCODE='42501'; END IF;
 IF p_case IS NULL OR p_version IS NULL OR p_id IS NULL
 OR (p_action IN ('add_reply','add_note') AND (p_body IS NULL OR length(btrim(p_body))=0 OR length(p_body)>4000))
 OR (p_action='set_priority' AND (p_body IS NULL OR p_body NOT IN ('low','normal','high','urgent')))
 OR (p_action NOT IN ('add_reply','add_note','set_priority') AND p_body IS NOT NULL)
 THEN RAISE EXCEPTION 'invalid action input' USING ERRCODE='42501'; END IF;
 IF p_action='add_note' AND NOT coalesce(claims->'gregale_internal_notes'='true'::jsonb,false) THEN RAISE EXCEPTION 'staff notes permission required' USING ERRCODE='42501'; END IF;
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
  UPDATE support_private.cases SET first_response_at=coalesce(first_response_at,date_trunc('milliseconds',clock_timestamp())) WHERE tenant=account AND id=p_case;
  INSERT INTO support_private.replies(tenant,case_id,body) VALUES(account,p_case,p_body) RETURNING id INTO reply_id;
 ELSIF p_action='add_note' THEN
  INSERT INTO support_private.notes(tenant,case_id,actor,body) VALUES(account,p_case,actor,p_body) RETURNING id INTO reply_id;
 ELSIF p_action='set_priority' THEN
  UPDATE support_private.cases SET priority=p_body WHERE tenant=account AND id=p_case;
  INSERT INTO support_private.priority_events(tenant,case_id,actor,previous_priority,priority,title)
  VALUES(account,p_case,actor,current_case.priority,p_body,'Priority changed to '||p_body);
 ELSIF p_action IN ('assign_case','unassign_case') THEN
  UPDATE support_private.cases SET assignee=CASE WHEN p_action='assign_case' THEN actor ELSE NULL END WHERE tenant=account AND id=p_case;
  INSERT INTO support_private.assignment_events(tenant,case_id,actor,previous_assignee,assignee,title)
  VALUES(account,p_case,actor,current_case.assignee,CASE WHEN p_action='assign_case' THEN actor ELSE NULL END,CASE WHEN p_action='assign_case' THEN 'Case assigned' ELSE 'Case unassigned' END);
 ELSE
  UPDATE support_private.cases SET status='closed' WHERE tenant=account AND id=p_case;
 END IF;
 UPDATE support_private.cases SET version=version+1 WHERE tenant=account AND id=p_case;
 answer:=jsonb_build_object('action',p_action,'version',p_version+1,'status',CASE WHEN p_action='close_case' THEN 'closed' ELSE 'open' END);
 IF p_action IN ('assign_case','unassign_case') THEN answer:=answer||jsonb_build_object('assignee',CASE WHEN p_action='assign_case' THEN actor ELSE NULL END); END IF;
 IF p_action='set_priority' THEN SELECT answer||jsonb_build_object('priority',priority,'response_due_at',response_due_at) INTO answer FROM support_private.cases WHERE tenant=account AND id=p_case; END IF;
 IF reply_id IS NOT NULL THEN answer:=answer||jsonb_build_object(CASE WHEN p_action='add_note' THEN 'note_id' ELSE 'reply_id' END,reply_id); END IF;
 -- Redacted actor/action/time/outcome and safe result commit with the mutation.
 INSERT INTO support_actions.receipts(id,tenant,subject,action,request_hash,result)
 VALUES(p_id,account,actor,p_action,fingerprint,answer);
 RETURN answer;
END $$;
