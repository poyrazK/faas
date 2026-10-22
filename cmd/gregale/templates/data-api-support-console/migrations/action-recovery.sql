-- Apply once after support-actions.sql. Revoke the runtime login's grant on the
-- renamed execute_mutation function, then grant only the new public functions.
ALTER FUNCTION support_actions.execute(text,integer,integer,text,uuid,timestamptz) RENAME TO execute_mutation;
CREATE TABLE support_actions.requests (
 id uuid PRIMARY KEY, tenant text NOT NULL, subject text NOT NULL,
 action text NOT NULL, request_hash text NOT NULL, deadline timestamptz NOT NULL,
 state text NOT NULL CHECK(state IN ('previewed','unresolved','completed','rejected')),
 result jsonb, time timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX ON support_actions.requests(tenant,subject,time DESC,id DESC);
REVOKE ALL ON support_actions.requests FROM PUBLIC;
-- Preserve completed outcomes created before recovery was installed.
INSERT INTO support_actions.requests(id,tenant,subject,action,request_hash,deadline,state,result,time)
 SELECT id,tenant,subject,action,request_hash,time+interval '5 minutes','completed',result,time
 FROM support_actions.receipts;
CREATE FUNCTION support_actions.register(p_action text,p_case integer,p_version integer,p_body text,p_id uuid,p_deadline timestamptz)
 RETURNS void LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog AS $$
DECLARE c jsonb:=current_setting('request.jwt.claims',true)::jsonb; fingerprint text;
BEGIN
 IF c->>'sub' IS NULL OR c->>'tenant' IS NULL OR p_action NOT IN ('add_reply','close_case')
 OR NOT coalesce(c->'gregale_support_actions' ? p_action,false) OR p_deadline IS NULL
 THEN RAISE EXCEPTION 'permission refused' USING ERRCODE='42501'; END IF;
 fingerprint:=encode(sha256(convert_to(jsonb_build_array(p_action,p_case,p_version,p_body)::text,'UTF8')),'hex');
 INSERT INTO support_actions.requests(id,tenant,subject,action,request_hash,deadline,state)
 VALUES(p_id,c->>'tenant',c->>'sub',p_action,fingerprint,p_deadline,'previewed');
END $$;
CREATE FUNCTION support_actions.submitted(p_id uuid)
 RETURNS void LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog AS $$
DECLARE c jsonb:=current_setting('request.jwt.claims',true)::jsonb;
BEGIN
 UPDATE support_actions.requests SET state='unresolved'
 WHERE id=p_id AND tenant=c->>'tenant' AND subject=c->>'sub' AND state='previewed'
 AND coalesce(c->'gregale_support_actions' ? action,false);
 IF NOT FOUND AND NOT EXISTS(SELECT 1 FROM support_actions.requests WHERE id=p_id AND tenant=c->>'tenant' AND subject=c->>'sub')
 THEN RAISE EXCEPTION 'request unavailable' USING ERRCODE='42501'; END IF;
END $$;
CREATE FUNCTION support_actions.execute(p_action text,p_case integer,p_version integer,p_body text,p_id uuid,p_deadline timestamptz)
 RETURNS jsonb LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog AS $$
DECLARE c jsonb:=current_setting('request.jwt.claims',true)::jsonb; r support_actions.requests; answer jsonb; fingerprint text;
BEGIN
 PERFORM pg_advisory_xact_lock(hashtextextended(p_id::text,0));
 SELECT * INTO r FROM support_actions.requests WHERE id=p_id FOR UPDATE;
 fingerprint:=encode(sha256(convert_to(jsonb_build_array(p_action,p_case,p_version,p_body)::text,'UTF8')),'hex');
 IF NOT FOUND OR r.tenant<>c->>'tenant' OR r.subject<>c->>'sub' OR r.action<>p_action
 OR r.request_hash<>fingerprint OR (r.state<>'completed' AND r.deadline<>p_deadline)
 OR NOT coalesce(c->'gregale_support_actions' ? p_action,false)
 THEN RAISE EXCEPTION 'permission refused' USING ERRCODE='42501'; END IF;
 IF r.state='rejected' THEN RETURN jsonb_build_object('rejected',true); END IF;
 BEGIN
  answer:=support_actions.execute_mutation(p_action,p_case,p_version,p_body,p_id,p_deadline);
 EXCEPTION WHEN serialization_failure OR no_data_found THEN
  UPDATE support_actions.requests SET state='rejected',result=NULL WHERE id=p_id;
  RETURN jsonb_build_object('rejected',true);
 END;
 UPDATE support_actions.requests SET state='completed',result=answer WHERE id=p_id;
 RETURN answer;
END $$;
CREATE FUNCTION support_actions.recent(p_id uuid DEFAULT NULL)
 RETURNS jsonb LANGUAGE sql SECURITY DEFINER SET search_path=pg_catalog AS $$
 SELECT coalesce(jsonb_agg(row),'[]'::jsonb) FROM (
 SELECT id,action,time,state,result FROM support_actions.requests
 WHERE tenant=current_setting('request.jwt.claims',true)::jsonb->>'tenant'
 AND subject=current_setting('request.jwt.claims',true)::jsonb->>'sub'
 AND (p_id IS NULL OR id=p_id) ORDER BY time DESC,id DESC LIMIT 50
 ) row
$$;
REVOKE ALL ON FUNCTION support_actions.register(text,integer,integer,text,uuid,timestamptz) FROM PUBLIC;
REVOKE ALL ON FUNCTION support_actions.submitted(uuid) FROM PUBLIC;
REVOKE ALL ON FUNCTION support_actions.execute(text,integer,integer,text,uuid,timestamptz) FROM PUBLIC;
REVOKE ALL ON FUNCTION support_actions.recent(uuid) FROM PUBLIC;
-- REVOKE EXECUTE ON FUNCTION support_actions.execute_mutation(text,integer,integer,text,uuid,timestamptz) FROM your_action_login;
-- GRANT EXECUTE ON FUNCTION support_actions.register(text,integer,integer,text,uuid,timestamptz),
-- support_actions.submitted(uuid),support_actions.execute(text,integer,integer,text,uuid,timestamptz),
-- support_actions.recent(uuid) TO your_action_login;
