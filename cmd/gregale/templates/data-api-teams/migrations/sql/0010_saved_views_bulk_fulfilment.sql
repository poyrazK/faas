-- Personal saved filters and reviewed, atomic bulk fulfilment are customer-owned workflows.
CREATE TABLE api.team_order_views (
  view_id uuid PRIMARY KEY,
  tenant_id text NOT NULL,
  subject text NOT NULL,
  name text NOT NULL CHECK(length(name) BETWEEN 1 AND 80),
  status_filter api.order_status,
  minimum_total integer CHECK(minimum_total>=0),
  maximum_total integer CHECK(maximum_total>=0),
  sort_order text NOT NULL CHECK(sort_order IN ('id_asc','id_desc','total_asc','total_desc')),
  revision uuid NOT NULL DEFAULT gen_random_uuid(),
  changed_at timestamptz NOT NULL DEFAULT clock_timestamp(),
  CHECK(minimum_total IS NULL OR maximum_total IS NULL OR minimum_total<=maximum_total),
  UNIQUE(tenant_id,subject,name)
);
CREATE TABLE api.team_order_view_receipts (
  tenant_id text NOT NULL,
  actor text NOT NULL,
  request_id uuid NOT NULL,
  view_id uuid NOT NULL,
  payload jsonb NOT NULL,
  snapshot jsonb NOT NULL,
  occurred_at timestamptz NOT NULL DEFAULT clock_timestamp(),
  PRIMARY KEY(tenant_id,actor,request_id)
);
CREATE TABLE api.team_bulk_order_operations (
  tenant_id text NOT NULL,
  actor text NOT NULL,
  operation_id uuid NOT NULL,
  action text NOT NULL CHECK(action='fulfil'),
  reviewed jsonb NOT NULL,
  result jsonb NOT NULL,
  occurred_at timestamptz NOT NULL DEFAULT clock_timestamp(),
  PRIMARY KEY(tenant_id,actor,operation_id)
);
CREATE INDEX personal_order_view_browse ON api.team_order_views(tenant_id,subject,view_id);
CREATE INDEX bulk_order_operation_browse ON api.team_bulk_order_operations(tenant_id,operation_id);
CREATE INDEX team_order_total_browse ON api.team_orders(tenant_id,total_cents,id);
ALTER TABLE api.team_order_views ENABLE ROW LEVEL SECURITY;
ALTER TABLE api.team_order_view_receipts ENABLE ROW LEVEL SECURITY;
ALTER TABLE api.team_bulk_order_operations ENABLE ROW LEVEL SECURITY;
CREATE POLICY personal_order_view_reads ON api.team_order_views FOR SELECT USING(
  subject=current_setting('request.jwt.claims',true)::jsonb->>'sub'
  AND EXISTS(SELECT 1 FROM api.team_members m WHERE m.tenant_id=team_order_views.tenant_id AND m.subject=team_order_views.subject)
);
CREATE POLICY personal_order_view_creates ON api.team_order_views FOR INSERT WITH CHECK(
  tenant_id=current_setting('gregale.order_view_write',true) AND subject=current_setting('request.jwt.claims',true)::jsonb->>'sub'
);
CREATE POLICY personal_order_view_updates ON api.team_order_views FOR UPDATE
  USING(tenant_id=current_setting('gregale.order_view_write',true) AND subject=current_setting('request.jwt.claims',true)::jsonb->>'sub')
  WITH CHECK(tenant_id=current_setting('gregale.order_view_write',true) AND subject=current_setting('request.jwt.claims',true)::jsonb->>'sub');
CREATE POLICY personal_order_view_receipts ON api.team_order_view_receipts FOR SELECT USING(
  actor=current_setting('request.jwt.claims',true)::jsonb->>'sub' AND (
    EXISTS(SELECT 1 FROM api.team_members m WHERE m.tenant_id=team_order_view_receipts.tenant_id AND m.subject=team_order_view_receipts.actor)
    OR tenant_id=current_setting('gregale.order_view_receipt',true))
);
CREATE POLICY reviewed_order_view_receipt_creates ON api.team_order_view_receipts FOR INSERT WITH CHECK(
  tenant_id=current_setting('gregale.order_view_write',true) AND actor=current_setting('request.jwt.claims',true)::jsonb->>'sub'
);
CREATE POLICY member_bulk_operation_reads ON api.team_bulk_order_operations FOR SELECT USING(
  EXISTS(SELECT 1 FROM api.team_members m WHERE m.tenant_id=team_bulk_order_operations.tenant_id AND m.subject=current_setting('request.jwt.claims',true)::jsonb->>'sub')
  OR tenant_id=current_setting('gregale.bulk_order_receipt',true) AND actor=current_setting('request.jwt.claims',true)::jsonb->>'sub'
);
CREATE POLICY reviewed_bulk_operation_creates ON api.team_bulk_order_operations FOR INSERT WITH CHECK(
  tenant_id=current_setting('gregale.bulk_order_write',true) AND actor=current_setting('request.jwt.claims',true)::jsonb->>'sub'
);

CREATE FUNCTION api.get_order_view_receipt(team_id text,operation_key uuid) RETURNS SETOF api.team_order_view_receipts
LANGUAGE plpgsql SECURITY INVOKER SET search_path=pg_catalog AS $$
DECLARE old_scope text:=current_setting('gregale.order_view_receipt',true);
BEGIN
  PERFORM set_config('gregale.order_view_receipt',COALESCE(team_id,''),true);
  RETURN QUERY SELECT r.* FROM api.team_order_view_receipts r WHERE r.tenant_id=team_id AND r.actor=current_setting('request.jwt.claims',true)::jsonb->>'sub' AND r.request_id=operation_key;
  PERFORM set_config('gregale.order_view_receipt',COALESCE(old_scope,''),true);
EXCEPTION WHEN OTHERS THEN PERFORM set_config('gregale.order_view_receipt',COALESCE(old_scope,''),true);RAISE;
END;
$$;
REVOKE EXECUTE ON FUNCTION api.get_order_view_receipt(text,uuid) FROM PUBLIC;
COMMENT ON FUNCTION api.get_order_view_receipt(text,uuid) IS '@gregale:rpc';

CREATE FUNCTION api.save_order_view_once(team_id text,request_id uuid,view_id uuid,expected_revision uuid,view_name text,
  status_filter api.order_status,minimum_total integer,maximum_total integer,sort_order text)
RETURNS SETOF api.team_order_view_receipts LANGUAGE plpgsql SECURITY INVOKER SET search_path=pg_catalog SET default_transaction_isolation='read committed' AS $$
DECLARE
  caller_subject text:=NULLIF(current_setting('request.jwt.claims',true)::jsonb->>'sub','');
  old_scope text:=current_setting('gregale.order_view_write',true);
  current_view api.team_order_views;
  receipt api.team_order_view_receipts;
  intended jsonb;
BEGIN
  IF caller_subject IS NULL OR team_id IS NULL OR team_id='' OR request_id IS NULL OR view_id IS NULL OR view_name IS NULL OR length(view_name) NOT BETWEEN 1 AND 80
    OR view_name<>btrim(view_name) OR btrim(view_name)='' OR minimum_total<0 OR maximum_total<0 OR minimum_total>maximum_total OR sort_order IS NULL OR sort_order NOT IN ('id_asc','id_desc','total_asc','total_desc') THEN
    RAISE SQLSTATE 'PT400' USING MESSAGE='invalid_order_view'; END IF;
  IF current_setting('transaction_isolation')<>'read committed' THEN RAISE SQLSTATE 'PT400' USING MESSAGE='order_read_committed_required'; END IF;
  PERFORM pg_advisory_xact_lock(hashtextextended('gregale:team-admin:'||team_id,0));
  IF NOT EXISTS(SELECT 1 FROM api.team_members m WHERE m.tenant_id=team_id AND m.subject=caller_subject) THEN RAISE SQLSTATE '42501' USING MESSAGE='team_membership_required'; END IF;
  intended:=jsonb_build_object('id',view_id,'revision',expected_revision,'name',view_name,'status',status_filter,'minimum',minimum_total,'maximum',maximum_total,'sort',sort_order);
  SELECT r.* INTO receipt FROM api.team_order_view_receipts r WHERE r.tenant_id=team_id AND r.actor=caller_subject AND r.request_id=save_order_view_once.request_id;
  IF FOUND THEN
    IF receipt.payload<>intended THEN RAISE SQLSTATE 'PT409' USING MESSAGE='order_view_payload_conflict'; END IF;
  ELSE
    SELECT v.* INTO current_view FROM api.team_order_views v WHERE v.tenant_id=team_id AND v.subject=caller_subject AND v.view_id=save_order_view_once.view_id;
    IF current_view.revision IS DISTINCT FROM expected_revision THEN RAISE SQLSTATE 'PT409' USING MESSAGE='order_view_revision_conflict'; END IF;
    PERFORM set_config('gregale.order_view_write',team_id,true);
    IF current_view.view_id IS NULL THEN
      INSERT INTO api.team_order_views(view_id,tenant_id,subject,name,status_filter,minimum_total,maximum_total,sort_order)
      VALUES(view_id,team_id,caller_subject,view_name,status_filter,minimum_total,maximum_total,sort_order) RETURNING * INTO current_view;
    ELSE
      UPDATE api.team_order_views v SET name=view_name,status_filter=save_order_view_once.status_filter,minimum_total=save_order_view_once.minimum_total,maximum_total=save_order_view_once.maximum_total,sort_order=save_order_view_once.sort_order,revision=gen_random_uuid(),changed_at=clock_timestamp()
      WHERE v.view_id=save_order_view_once.view_id AND v.tenant_id=team_id AND v.subject=caller_subject RETURNING v.* INTO current_view;
    END IF;
    INSERT INTO api.team_order_view_receipts(tenant_id,actor,request_id,view_id,payload,snapshot)
      VALUES(team_id,caller_subject,request_id,view_id,intended,to_jsonb(current_view)) RETURNING * INTO receipt;
  END IF;
  PERFORM set_config('gregale.order_view_write',COALESCE(old_scope,''),true);RETURN NEXT receipt;
EXCEPTION WHEN OTHERS THEN PERFORM set_config('gregale.order_view_write',COALESCE(old_scope,''),true);RAISE;
END;
$$;
REVOKE EXECUTE ON FUNCTION api.save_order_view_once(text,uuid,uuid,uuid,text,api.order_status,integer,integer,text) FROM PUBLIC;
COMMENT ON FUNCTION api.save_order_view_once(text,uuid,uuid,uuid,text,api.order_status,integer,integer,text) IS '@gregale:rpc';

CREATE FUNCTION api.list_view_orders(team_id text,status_filter api.order_status DEFAULT NULL,minimum_total integer DEFAULT NULL,maximum_total integer DEFAULT NULL,
  sort_order text DEFAULT 'id_asc',after_id uuid DEFAULT NULL,after_total integer DEFAULT NULL,page_size integer DEFAULT 20)
RETURNS SETOF api.team_orders LANGUAGE plpgsql STABLE SECURITY INVOKER SET search_path=pg_catalog AS $$
BEGIN
  IF team_id IS NULL OR team_id='' OR minimum_total<0 OR maximum_total<0 OR minimum_total>maximum_total OR sort_order IS NULL OR sort_order NOT IN ('id_asc','id_desc','total_asc','total_desc')
    OR page_size IS NULL OR page_size NOT BETWEEN 1 AND 100 OR after_total<0 OR after_id IS NULL AND after_total IS NOT NULL
    OR after_id IS NOT NULL AND ((sort_order IN ('total_asc','total_desc'))<>(after_total IS NOT NULL)) THEN RAISE SQLSTATE 'PT400' USING MESSAGE='invalid_order_view'; END IF;
  IF NOT EXISTS(SELECT 1 FROM api.team_members m WHERE m.tenant_id=team_id AND m.subject=current_setting('request.jwt.claims',true)::jsonb->>'sub') THEN RAISE SQLSTATE '42501' USING MESSAGE='team_membership_required'; END IF;
  RETURN QUERY SELECT o.* FROM api.team_orders o WHERE o.tenant_id=team_id AND (status_filter IS NULL OR o.status=status_filter)
    AND (minimum_total IS NULL OR o.total_cents>=minimum_total) AND (maximum_total IS NULL OR o.total_cents<=maximum_total)
    AND (after_id IS NULL OR sort_order='id_asc' AND o.id>after_id OR sort_order='id_desc' AND o.id<after_id
      OR sort_order='total_asc' AND (o.total_cents>after_total OR o.total_cents=after_total AND o.id>after_id)
      OR sort_order='total_desc' AND (o.total_cents<after_total OR o.total_cents=after_total AND o.id<after_id))
    ORDER BY CASE WHEN sort_order='total_asc' THEN o.total_cents END ASC,CASE WHEN sort_order='total_desc' THEN o.total_cents END DESC,
      CASE WHEN sort_order IN ('id_asc','total_asc') THEN o.id END ASC,CASE WHEN sort_order IN ('id_desc','total_desc') THEN o.id END DESC LIMIT page_size+1;
END;
$$;
REVOKE EXECUTE ON FUNCTION api.list_view_orders(text,api.order_status,integer,integer,text,uuid,integer,integer) FROM PUBLIC;
COMMENT ON FUNCTION api.list_view_orders(text,api.order_status,integer,integer,text,uuid,integer,integer) IS '@gregale:rpc';

CREATE FUNCTION api.get_team_bulk_order_receipt(team_id text,operation_key uuid) RETURNS SETOF api.team_bulk_order_operations
LANGUAGE plpgsql SECURITY INVOKER SET search_path=pg_catalog AS $$
DECLARE old_scope text:=current_setting('gregale.bulk_order_receipt',true);
BEGIN
  PERFORM set_config('gregale.bulk_order_receipt',COALESCE(team_id,''),true);
  RETURN QUERY SELECT r.* FROM api.team_bulk_order_operations r WHERE r.tenant_id=team_id AND r.actor=current_setting('request.jwt.claims',true)::jsonb->>'sub' AND r.operation_id=operation_key;
  PERFORM set_config('gregale.bulk_order_receipt',COALESCE(old_scope,''),true);
EXCEPTION WHEN OTHERS THEN PERFORM set_config('gregale.bulk_order_receipt',COALESCE(old_scope,''),true);RAISE;
END;
$$;
REVOKE EXECUTE ON FUNCTION api.get_team_bulk_order_receipt(text,uuid) FROM PUBLIC;
COMMENT ON FUNCTION api.get_team_bulk_order_receipt(text,uuid) IS '@gregale:rpc';

CREATE FUNCTION api.preview_team_order_fulfilment(team_id text,reviewed jsonb) RETURNS jsonb
LANGUAGE plpgsql STABLE SECURITY INVOKER SET search_path=pg_catalog AS $$
DECLARE item jsonb;intended jsonb;target api.team_orders;result jsonb:='[]'::jsonb;reason text;
BEGIN
  IF team_id IS NULL OR team_id='' THEN RAISE SQLSTATE 'PT400' USING MESSAGE='invalid_order_selection'; END IF;
  IF NOT EXISTS(SELECT 1 FROM api.team_members m WHERE m.tenant_id=team_id AND m.subject=current_setting('request.jwt.claims',true)::jsonb->>'sub' AND m.role IN ('owner','admin')) THEN RAISE SQLSTATE '42501' USING MESSAGE='team_write_denied'; END IF;
  IF reviewed IS NULL OR jsonb_typeof(reviewed)<>'array' THEN RAISE SQLSTATE 'PT400' USING MESSAGE='invalid_order_selection'; END IF;
  IF jsonb_array_length(reviewed) NOT BETWEEN 1 AND 20 OR octet_length(reviewed::text)>8192 THEN RAISE SQLSTATE 'PT400' USING MESSAGE='invalid_order_selection'; END IF;
  FOR item IN SELECT value FROM jsonb_array_elements(reviewed) LOOP
    IF jsonb_typeof(item)<>'object' THEN RAISE SQLSTATE 'PT400' USING MESSAGE='invalid_order_selection'; END IF;
    IF (SELECT count(*) FROM jsonb_object_keys(item))<>2 OR jsonb_typeof(item->'id') IS DISTINCT FROM 'string' OR (item->>'id') !~ '^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$'
      OR jsonb_typeof(item->'version') IS DISTINCT FROM 'number' OR (item->>'version') !~ '^[0-9]+$' THEN RAISE SQLSTATE 'PT400' USING MESSAGE='invalid_order_selection'; END IF;
    IF (item->>'version')::numeric NOT BETWEEN 1 AND 2147483646 THEN RAISE SQLSTATE 'PT400' USING MESSAGE='invalid_order_selection'; END IF;
  END LOOP;
  IF (SELECT count(DISTINCT value->>'id') FROM jsonb_array_elements(reviewed))<>jsonb_array_length(reviewed) THEN RAISE SQLSTATE 'PT400' USING MESSAGE='invalid_order_selection'; END IF;
  SELECT jsonb_agg(value ORDER BY value->>'id') INTO intended FROM jsonb_array_elements(reviewed);
  FOR item IN SELECT value FROM jsonb_array_elements(intended) LOOP
    SELECT o.* INTO target FROM api.team_orders o WHERE o.tenant_id=team_id AND o.id=(item->>'id')::uuid;
    reason:=CASE WHEN target.id IS NULL THEN 'unavailable' WHEN target.version<>(item->>'version')::integer THEN 'changed' WHEN target.status<>'submitted' THEN 'not_submitted' ELSE 'eligible' END;
    result:=result||jsonb_build_array(jsonb_build_object('id',item->>'id','expected_version',(item->>'version')::integer,'reason',reason,'current',CASE WHEN target.id IS NULL THEN NULL ELSE to_jsonb(target) END));
  END LOOP;
  RETURN result;
END;
$$;
REVOKE EXECUTE ON FUNCTION api.preview_team_order_fulfilment(text,jsonb) FROM PUBLIC;
COMMENT ON FUNCTION api.preview_team_order_fulfilment(text,jsonb) IS '@gregale:rpc';

CREATE FUNCTION api.fulfil_team_orders_once(team_id text,operation_id uuid,reviewed jsonb) RETURNS SETOF api.team_bulk_order_operations
LANGUAGE plpgsql SECURITY INVOKER SET search_path=pg_catalog SET default_transaction_isolation='read committed' AS $$
DECLARE
  caller_subject text:=NULLIF(current_setting('request.jwt.claims',true)::jsonb->>'sub','');
  old_write text:=current_setting('gregale.team_order_write',true);
  old_transition text:=current_setting('gregale.order_transition',true);
  old_bulk text:=current_setting('gregale.bulk_order_write',true);
  receipt api.team_bulk_order_operations;
  target api.team_orders;
  item jsonb;intended jsonb;results jsonb:='[]'::jsonb;
BEGIN
  IF caller_subject IS NULL OR team_id IS NULL OR team_id='' OR operation_id IS NULL THEN RAISE SQLSTATE 'PT400' USING MESSAGE='invalid_order_selection'; END IF;
  IF current_setting('transaction_isolation')<>'read committed' THEN RAISE SQLSTATE 'PT400' USING MESSAGE='order_read_committed_required'; END IF;
  PERFORM pg_advisory_xact_lock(hashtextextended('gregale:team-admin:'||team_id,0));
  IF NOT EXISTS(SELECT 1 FROM api.team_members m WHERE m.tenant_id=team_id AND m.subject=caller_subject AND m.role IN ('owner','admin')) THEN RAISE SQLSTATE '42501' USING MESSAGE='team_write_denied'; END IF;
  IF reviewed IS NULL OR jsonb_typeof(reviewed)<>'array' THEN RAISE SQLSTATE 'PT400' USING MESSAGE='invalid_order_selection'; END IF;
  IF jsonb_array_length(reviewed) NOT BETWEEN 1 AND 20 OR octet_length(reviewed::text)>8192 THEN RAISE SQLSTATE 'PT400' USING MESSAGE='invalid_order_selection'; END IF;
  FOR item IN SELECT value FROM jsonb_array_elements(reviewed) LOOP
    IF jsonb_typeof(item)<>'object' THEN RAISE SQLSTATE 'PT400' USING MESSAGE='invalid_order_selection'; END IF;
    IF (SELECT count(*) FROM jsonb_object_keys(item))<>2 OR jsonb_typeof(item->'id') IS DISTINCT FROM 'string' OR (item->>'id') !~ '^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$'
      OR jsonb_typeof(item->'version') IS DISTINCT FROM 'number' OR (item->>'version') !~ '^[0-9]+$' THEN RAISE SQLSTATE 'PT400' USING MESSAGE='invalid_order_selection'; END IF;
    IF (item->>'version')::numeric NOT BETWEEN 1 AND 2147483646 THEN RAISE SQLSTATE 'PT400' USING MESSAGE='invalid_order_selection'; END IF;
  END LOOP;
  IF (SELECT count(DISTINCT value->>'id') FROM jsonb_array_elements(reviewed))<>jsonb_array_length(reviewed) THEN RAISE SQLSTATE 'PT400' USING MESSAGE='invalid_order_selection'; END IF;
  SELECT jsonb_agg(value ORDER BY value->>'id') INTO intended FROM jsonb_array_elements(reviewed);
  SELECT r.* INTO receipt FROM api.team_bulk_order_operations r WHERE r.tenant_id=team_id AND r.actor=caller_subject AND r.operation_id=fulfil_team_orders_once.operation_id;
  IF FOUND THEN
    IF receipt.reviewed<>intended THEN RAISE SQLSTATE 'PT409' USING MESSAGE='bulk_order_payload_conflict'; END IF;
  ELSE
    PERFORM set_config('gregale.team_order_write','on',true);
    PERFORM set_config('gregale.order_transition','fulfilled',true);
    PERFORM set_config('gregale.bulk_order_write',team_id,true);
    FOR item IN SELECT value FROM jsonb_array_elements(intended) LOOP
      UPDATE api.team_orders o SET status='fulfilled',version=o.version+1
        WHERE o.tenant_id=team_id AND o.id=(item->>'id')::uuid AND o.version=(item->>'version')::integer AND o.status='submitted' RETURNING o.* INTO target;
      IF NOT FOUND THEN RAISE SQLSTATE 'PT409' USING MESSAGE='bulk_order_preview_stale'; END IF;
      results:=results||jsonb_build_array(to_jsonb(target));
    END LOOP;
    INSERT INTO api.team_bulk_order_operations(tenant_id,actor,operation_id,action,reviewed,result)
      VALUES(team_id,caller_subject,operation_id,'fulfil',intended,results) RETURNING * INTO receipt;
  END IF;
  PERFORM set_config('gregale.team_order_write',COALESCE(old_write,''),true);
  PERFORM set_config('gregale.order_transition',COALESCE(old_transition,''),true);
  PERFORM set_config('gregale.bulk_order_write',COALESCE(old_bulk,''),true);
  RETURN NEXT receipt;
EXCEPTION WHEN OTHERS THEN
  PERFORM set_config('gregale.team_order_write',COALESCE(old_write,''),true);
  PERFORM set_config('gregale.order_transition',COALESCE(old_transition,''),true);
  PERFORM set_config('gregale.bulk_order_write',COALESCE(old_bulk,''),true);RAISE;
END;
$$;
REVOKE EXECUTE ON FUNCTION api.fulfil_team_orders_once(text,uuid,jsonb) FROM PUBLIC;
COMMENT ON FUNCTION api.fulfil_team_orders_once(text,uuid,jsonb) IS '@gregale:rpc';
