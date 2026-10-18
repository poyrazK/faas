-- Subject-scoped metadata feed (ADR-1676). Fixed bounds mirror
-- DataAPINoteChangeFeed* in pkg/api/limits.go. No historical note bodies.
CREATE TABLE api.note_change_feed (
  singleton boolean PRIMARY KEY DEFAULT true CHECK (singleton),
  epoch uuid NOT NULL DEFAULT gen_random_uuid()
);
INSERT INTO api.note_change_feed(singleton) VALUES(true);
ALTER TABLE api.note_change_feed ENABLE ROW LEVEL SECURITY;
CREATE POLICY scoped_feed_read ON api.note_change_feed FOR SELECT USING (
  current_setting('gregale.note_change_scope',true)='read'
  AND NULLIF(current_setting('request.jwt.claims',true)::jsonb->>'sub','') IS NOT NULL
);

CREATE TABLE api.note_change_streams (
  subject text PRIMARY KEY,
  head bigint NOT NULL CHECK (head > 0),
  discarded_through bigint NOT NULL CHECK (discarded_through=greatest(0::bigint,head-10000))
);
ALTER TABLE api.note_change_streams ENABLE ROW LEVEL SECURITY;
CREATE POLICY scoped_stream_read ON api.note_change_streams FOR SELECT USING (
  subject=current_setting('request.jwt.claims',true)::jsonb->>'sub'
  AND current_setting('gregale.note_change_scope',true) IN ('read','capture')
);
CREATE POLICY scoped_stream_insert ON api.note_change_streams FOR INSERT WITH CHECK (
  subject=current_setting('request.jwt.claims',true)::jsonb->>'sub'
  AND current_setting('gregale.note_change_scope',true)='capture'
);
CREATE POLICY scoped_stream_update ON api.note_change_streams FOR UPDATE USING (
  subject=current_setting('request.jwt.claims',true)::jsonb->>'sub'
  AND current_setting('gregale.note_change_scope',true)='capture'
) WITH CHECK (
  subject=current_setting('request.jwt.claims',true)::jsonb->>'sub'
  AND current_setting('gregale.note_change_scope',true)='capture'
);

CREATE TABLE api.note_changes (
  subject text NOT NULL,
  position bigint NOT NULL CHECK (position > 0),
  operation text NOT NULL CHECK (operation IN ('insert','update','delete')),
  note_id integer NOT NULL CHECK (note_id > 0),
  version integer NOT NULL CHECK (version > 0),
  PRIMARY KEY(subject,position)
);
ALTER TABLE api.note_changes ENABLE ROW LEVEL SECURITY;
CREATE POLICY scoped_changes_read ON api.note_changes FOR SELECT USING (
  subject=current_setting('request.jwt.claims',true)::jsonb->>'sub'
  AND current_setting('gregale.note_change_scope',true) IN ('read','capture')
);
CREATE POLICY scoped_changes_insert ON api.note_changes FOR INSERT WITH CHECK (
  subject=current_setting('request.jwt.claims',true)::jsonb->>'sub'
  AND current_setting('gregale.note_change_scope',true)='capture'
);
CREATE POLICY scoped_changes_expiry ON api.note_changes FOR DELETE USING (
  subject=current_setting('request.jwt.claims',true)::jsonb->>'sub'
  AND current_setting('gregale.note_change_scope',true)='capture'
  AND position <= (SELECT s.discarded_through FROM api.note_change_streams s WHERE s.subject=note_changes.subject)
);

-- Preserve the stream watermark after note deletion. Ordinary owner DML must
-- not rewind it or remove live events. Trusted owners can still change schema.
CREATE FUNCTION api.guard_note_change_stream() RETURNS trigger
LANGUAGE plpgsql SECURITY INVOKER SET search_path=pg_catalog AS $$
BEGIN
  IF current_setting('gregale.note_change_scope',true) IS DISTINCT FROM 'capture' OR TG_OP='DELETE' THEN
    RAISE SQLSTATE '42501' USING MESSAGE='note_change_stream_managed';
  END IF;
  IF TG_OP='UPDATE' AND (NEW.subject IS DISTINCT FROM OLD.subject OR NEW.head<=OLD.head) THEN
    RAISE SQLSTATE '42501' USING MESSAGE='note_change_stream_managed';
  END IF;
  RETURN NEW;
END;
$$;
REVOKE ALL ON FUNCTION api.guard_note_change_stream() FROM PUBLIC;
CREATE TRIGGER guard_note_change_stream BEFORE INSERT OR UPDATE OR DELETE ON api.note_change_streams
  FOR EACH ROW EXECUTE FUNCTION api.guard_note_change_stream();

CREATE FUNCTION api.guard_note_change_event() RETURNS trigger
LANGUAGE plpgsql SECURITY INVOKER SET search_path=pg_catalog AS $$
BEGIN
  IF TG_OP='UPDATE' OR current_setting('gregale.note_change_scope',true) IS DISTINCT FROM 'capture' THEN
    RAISE SQLSTATE '42501' USING MESSAGE='note_change_event_managed';
  END IF;
  IF TG_OP='DELETE' AND NOT EXISTS(SELECT 1 FROM api.note_change_streams s
      WHERE s.subject=OLD.subject AND OLD.position<=s.discarded_through) THEN
    RAISE SQLSTATE '42501' USING MESSAGE='note_change_event_managed';
  END IF;
  IF TG_OP='DELETE' THEN RETURN OLD; END IF;
  RETURN NEW;
END;
$$;
REVOKE ALL ON FUNCTION api.guard_note_change_event() FROM PUBLIC;
CREATE TRIGGER guard_note_change_event BEFORE INSERT OR UPDATE OR DELETE ON api.note_changes
  FOR EACH ROW EXECUTE FUNCTION api.guard_note_change_event();

CREATE FUNCTION api.capture_note_change() RETURNS trigger
LANGUAGE plpgsql SECURITY INVOKER SET search_path=pg_catalog AS $$
DECLARE
  previous_scope text:=current_setting('gregale.note_change_scope',true);
  groups jsonb;
  batch record;
  batch_size bigint;
  cursor_position bigint;
  cursor_floor bigint;
BEGIN
  -- Transition tables contain only rows actually changed by this statement,
  -- after row triggers. Group once and allocate each subject's contiguous range
  -- once: a large INSERT must not update one counter thousands of times.
  IF TG_OP='INSERT' THEN
    SELECT jsonb_agg(jsonb_build_object('subject',subject,'events',events) ORDER BY subject COLLATE "C") INTO groups
      FROM (SELECT subject,jsonb_agg(jsonb_build_object('operation','insert','note_id',id,'version',version) ORDER BY id) AS events
        FROM note_change_new GROUP BY subject) g;
  ELSIF TG_OP='DELETE' THEN
    SELECT jsonb_agg(jsonb_build_object('subject',subject,'events',events) ORDER BY subject COLLATE "C") INTO groups
      FROM (SELECT subject,jsonb_agg(jsonb_build_object('operation','delete','note_id',id,'version',version) ORDER BY id) AS events
        FROM note_change_old GROUP BY subject) g;
  ELSIF TG_OP='UPDATE' THEN
    SELECT jsonb_agg(jsonb_build_object('subject',subject,'events',events) ORDER BY subject COLLATE "C") INTO groups
      FROM (SELECT subject,jsonb_agg(jsonb_build_object('operation',operation,'note_id',id,'version',version) ORDER BY id,operation) AS events
        FROM (SELECT subject,id,COALESCE(max(version) FILTER(WHERE is_new),max(version)) AS version,
          CASE WHEN bool_or(is_new) AND bool_or(NOT is_new) THEN 'update'
            WHEN bool_or(is_new) THEN 'insert' ELSE 'delete' END AS operation
          FROM (SELECT subject,id,version,true AS is_new FROM note_change_new
            UNION ALL SELECT subject,id,version,false FROM note_change_old) identities
          GROUP BY subject,id
        ) changes GROUP BY subject) g;
  END IF;
  PERFORM set_config('gregale.note_change_scope','capture',true);
  FOR batch IN SELECT e.value->>'subject' AS subject,e.value->'events' AS events
      FROM jsonb_array_elements(COALESCE(groups,'[]'::jsonb)) WITH ORDINALITY e ORDER BY e.ordinality LOOP
    batch_size:=jsonb_array_length(batch.events);
    -- This transactional row lock is held until commit. A later writer for
    -- this subject cannot allocate/commit a higher position ahead of us.
    INSERT INTO api.note_change_streams AS s(subject,head,discarded_through)
      VALUES(batch.subject,batch_size,greatest(0::bigint,batch_size-10000))
      ON CONFLICT(subject) DO UPDATE SET head=s.head+batch_size, discarded_through=greatest(0::bigint,s.head+batch_size-10000)
      RETURNING head,discarded_through INTO cursor_position,cursor_floor;
    INSERT INTO api.note_changes(subject,position,operation,note_id,version)
      SELECT batch.subject,cursor_position-batch_size+e.ordinality,e.value->>'operation',
        (e.value->>'note_id')::integer,(e.value->>'version')::integer
      FROM jsonb_array_elements(batch.events) WITH ORDINALITY e
      WHERE cursor_position-batch_size+e.ordinality>cursor_floor;
    IF cursor_floor>0 THEN
      DELETE FROM api.note_changes c WHERE c.subject=batch.subject AND c.position<=cursor_floor;
    END IF;
  END LOOP;
  PERFORM set_config('gregale.note_change_scope',COALESCE(previous_scope,''),true);
  RETURN NULL;
EXCEPTION WHEN OTHERS THEN
  PERFORM set_config('gregale.note_change_scope',COALESCE(previous_scope,''),true);
  RAISE;
END;
$$;
REVOKE ALL ON FUNCTION api.capture_note_change() FROM PUBLIC;
CREATE TRIGGER capture_note_insert AFTER INSERT ON api.notes REFERENCING NEW TABLE AS note_change_new
  FOR EACH STATEMENT EXECUTE FUNCTION api.capture_note_change();
CREATE TRIGGER capture_note_update AFTER UPDATE ON api.notes REFERENCING OLD TABLE AS note_change_old NEW TABLE AS note_change_new
  FOR EACH STATEMENT EXECUTE FUNCTION api.capture_note_change();
CREATE TRIGGER capture_note_delete AFTER DELETE ON api.notes REFERENCING OLD TABLE AS note_change_old
  FOR EACH STATEMENT EXECUTE FUNCTION api.capture_note_change();

CREATE TYPE api.note_sync_checkpoint AS (cursor text);
CREATE TYPE api.note_change_page AS (next_cursor text,has_more boolean,changes jsonb);

-- Capture a checkpoint BEFORE scanning current notes by id. Then replay the
-- metadata feed and re-fetch affected ids through ordinary RLS-protected reads.
-- This is read-only even when the subject has never written a note.
CREATE FUNCTION api.start_note_sync() RETURNS SETOF api.note_sync_checkpoint
LANGUAGE plpgsql STABLE SECURITY INVOKER SET search_path=pg_catalog AS $$
DECLARE
  caller text:=NULLIF(current_setting('request.jwt.claims',true)::jsonb->>'sub','');
  previous_scope text:=current_setting('gregale.note_change_scope',true);
  feed_epoch uuid;
  stream_head bigint;
  binding text;
BEGIN
  IF caller IS NULL THEN RAISE SQLSTATE '42501' USING MESSAGE='application subject required'; END IF;
  PERFORM set_config('gregale.note_change_scope','read',true);
  SELECT f.epoch INTO feed_epoch FROM api.note_change_feed f WHERE f.singleton;
  IF feed_epoch IS NULL THEN RAISE SQLSTATE 'PT503' USING MESSAGE='note_change_feed_unavailable'; END IF;
  SELECT s.head INTO stream_head FROM api.note_change_streams s WHERE s.subject=caller;
  binding:=encode(sha256(convert_to(jsonb_build_array(feed_epoch,caller)::text,'UTF8')),'hex');
  PERFORM set_config('gregale.note_change_scope',COALESCE(previous_scope,''),true);
  RETURN NEXT ROW('v1.'||feed_epoch::text||'.'||binding||'.'||COALESCE(stream_head,0)::text)::api.note_sync_checkpoint;
EXCEPTION WHEN OTHERS THEN
  PERFORM set_config('gregale.note_change_scope',COALESCE(previous_scope,''),true);
  RAISE;
END;
$$;
REVOKE ALL ON FUNCTION api.start_note_sync() FROM PUBLIC;
COMMENT ON FUNCTION api.start_note_sync() IS '@gregale:rpc';

CREATE FUNCTION api.read_note_changes(after_cursor text,page_size integer DEFAULT 100) RETURNS SETOF api.note_change_page
LANGUAGE plpgsql STABLE SECURITY INVOKER SET search_path=pg_catalog AS $$
DECLARE
  caller text:=NULLIF(current_setting('request.jwt.claims',true)::jsonb->>'sub','');
  previous_scope text:=current_setting('gregale.note_change_scope',true);
  feed_epoch uuid;
  prefix text;
  position_text text;
  after_position bigint;
  stream_head bigint;
  stream_floor bigint;
  next_position bigint;
  page jsonb;
BEGIN
  IF caller IS NULL THEN RAISE SQLSTATE '42501' USING MESSAGE='application subject required'; END IF;
  IF page_size IS NULL OR page_size<1 OR page_size>100 THEN
    RAISE SQLSTATE 'PT400' USING MESSAGE='note_change_page_size_invalid';
  END IF;
  IF after_cursor IS NULL OR octet_length(after_cursor)>128 THEN
    RAISE SQLSTATE 'PT400' USING MESSAGE='note_change_cursor_invalid';
  END IF;
  PERFORM set_config('gregale.note_change_scope','read',true);
  SELECT f.epoch INTO feed_epoch FROM api.note_change_feed f WHERE f.singleton;
  IF feed_epoch IS NULL THEN RAISE SQLSTATE 'PT503' USING MESSAGE='note_change_feed_unavailable'; END IF;
  prefix:='v1.'||feed_epoch::text||'.'||encode(sha256(convert_to(jsonb_build_array(feed_epoch,caller)::text,'UTF8')),'hex')||'.';
  IF left(after_cursor,length(prefix))<>prefix THEN
    RAISE SQLSTATE 'PT400' USING MESSAGE='note_change_cursor_invalid';
  END IF;
  position_text:=substr(after_cursor,length(prefix)+1);
  IF position_text !~ '^(0|[1-9][0-9]{0,18})$' OR position_text ~ '[^0-9]' THEN
    RAISE SQLSTATE 'PT400' USING MESSAGE='note_change_cursor_invalid';
  END IF;
  IF position_text::numeric>9223372036854775807 THEN
    RAISE SQLSTATE 'PT400' USING MESSAGE='note_change_cursor_invalid';
  END IF;
  after_position:=position_text::bigint;
  SELECT s.head,s.discarded_through INTO stream_head,stream_floor FROM api.note_change_streams s WHERE s.subject=caller;
  stream_head:=COALESCE(stream_head,0); stream_floor:=COALESCE(stream_floor,0);
  IF after_position<stream_floor THEN RAISE SQLSTATE 'PT410' USING MESSAGE='note_change_cursor_expired'; END IF;
  IF after_position>stream_head THEN RAISE SQLSTATE 'PT400' USING MESSAGE='note_change_cursor_invalid'; END IF;
  SELECT COALESCE(jsonb_agg(jsonb_build_object('sequence',c.position::text,'operation',c.operation,
      'note_id',c.note_id,'version',c.version) ORDER BY c.position),'[]'::jsonb),COALESCE(max(c.position),after_position)
    INTO page,next_position FROM (SELECT e.* FROM api.note_changes e
      WHERE e.subject=caller AND e.position>after_position AND e.position<=stream_head ORDER BY e.position LIMIT page_size) c;
  IF jsonb_array_length(page)<>least(page_size::bigint,stream_head-after_position)
      OR next_position<>after_position+jsonb_array_length(page) THEN
    RAISE SQLSTATE 'PT503' USING MESSAGE='note_change_feed_unavailable';
  END IF;
  PERFORM set_config('gregale.note_change_scope',COALESCE(previous_scope,''),true);
  RETURN NEXT ROW(prefix||next_position::text,next_position<stream_head,page)::api.note_change_page;
EXCEPTION WHEN OTHERS THEN
  PERFORM set_config('gregale.note_change_scope',COALESCE(previous_scope,''),true);
  RAISE;
END;
$$;
REVOKE ALL ON FUNCTION api.read_note_changes(text,integer) FROM PUBLIC;
COMMENT ON FUNCTION api.read_note_changes(text,integer) IS '@gregale:rpc';
