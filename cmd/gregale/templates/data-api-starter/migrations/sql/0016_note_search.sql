-- Ranked English AND-word search (ADR-1677). Fixed bounds mirror
-- DataAPINoteSearch* in pkg/api/limits.go. Equality postings remain indexable
-- under RLS; PostgreSQL's non-leakproof @@ predicate is a final row check.
CREATE TABLE api.note_search_terms (
  subject text NOT NULL,
  term text NOT NULL CHECK (term <> ''),
  note_id integer NOT NULL,
  PRIMARY KEY(subject,term,note_id)
);
ALTER TABLE api.note_search_terms ENABLE ROW LEVEL SECURITY;
CREATE POLICY scoped_search_read ON api.note_search_terms FOR SELECT USING (
  subject=current_setting('request.jwt.claims',true)::jsonb->>'sub'
  AND current_setting('gregale.note_search_scope',true) IN ('read','capture')
);
CREATE POLICY scoped_search_insert ON api.note_search_terms FOR INSERT WITH CHECK (
  subject=current_setting('request.jwt.claims',true)::jsonb->>'sub'
  AND current_setting('gregale.note_search_scope',true)='capture' AND pg_trigger_depth()>0
);
CREATE POLICY scoped_search_delete ON api.note_search_terms FOR DELETE USING (
  subject=current_setting('request.jwt.claims',true)::jsonb->>'sub'
  AND current_setting('gregale.note_search_scope',true)='capture' AND pg_trigger_depth()>0
);

-- Backfill existing notes before installing the direct-DML guard.
INSERT INTO api.note_search_terms(subject,term,note_id)
  SELECT n.subject,t.term,n.id FROM api.notes n
  CROSS JOIN LATERAL unnest(tsvector_to_array(to_tsvector('pg_catalog.english',n.body))) t(term);

CREATE FUNCTION api.guard_note_search_term() RETURNS trigger
LANGUAGE plpgsql SECURITY INVOKER SET search_path=pg_catalog AS $$
BEGIN
  IF TG_OP='UPDATE' OR pg_trigger_depth()<2 OR current_setting('gregale.note_search_scope',true) IS DISTINCT FROM 'capture' THEN
    RAISE SQLSTATE '42501' USING MESSAGE='note_search_index_managed';
  END IF;
  IF TG_OP='DELETE' THEN RETURN OLD; END IF;
  RETURN NEW;
END;
$$;
REVOKE ALL ON FUNCTION api.guard_note_search_term() FROM PUBLIC;
CREATE TRIGGER guard_note_search_term BEFORE INSERT OR UPDATE OR DELETE ON api.note_search_terms
  FOR EACH ROW EXECUTE FUNCTION api.guard_note_search_term();

CREATE FUNCTION api.refresh_note_search_terms() RETURNS trigger
LANGUAGE plpgsql SECURITY INVOKER SET search_path=pg_catalog AS $$
DECLARE
  previous_scope text:=current_setting('gregale.note_search_scope',true);
  batch record;
BEGIN
  PERFORM set_config('gregale.note_search_scope','capture',true);
  IF TG_OP<>'INSERT' THEN
    -- One indexed identity-array delete per affected subject avoids both a
    -- cached row-trigger full scan and quadratic transition-table join plans.
    FOR batch IN SELECT o.subject,array_agg(o.id) AS ids FROM note_search_old o
        GROUP BY o.subject ORDER BY o.subject COLLATE "C" LOOP
      DELETE FROM api.note_search_terms p WHERE p.subject=batch.subject AND p.note_id=ANY(batch.ids);
    END LOOP;
  END IF;
  IF TG_OP<>'DELETE' THEN
    INSERT INTO api.note_search_terms(subject,term,note_id)
      SELECT n.subject,t.term,n.id FROM note_search_new n
      CROSS JOIN LATERAL unnest(tsvector_to_array(to_tsvector('pg_catalog.english',n.body))) t(term);
  END IF;
  PERFORM set_config('gregale.note_search_scope',COALESCE(previous_scope,''),true);
  RETURN NULL;
EXCEPTION WHEN OTHERS THEN
  PERFORM set_config('gregale.note_search_scope',COALESCE(previous_scope,''),true);
  RAISE;
END;
$$;
REVOKE ALL ON FUNCTION api.refresh_note_search_terms() FROM PUBLIC;
-- Also index by identity for update/delete maintenance, independent of terms.
CREATE INDEX note_search_identity_idx ON api.note_search_terms(subject,note_id);
CREATE TRIGGER refresh_note_search_insert AFTER INSERT ON api.notes REFERENCING NEW TABLE AS note_search_new
  FOR EACH STATEMENT EXECUTE FUNCTION api.refresh_note_search_terms();
CREATE TRIGGER refresh_note_search_update AFTER UPDATE ON api.notes REFERENCING OLD TABLE AS note_search_old NEW TABLE AS note_search_new
  FOR EACH STATEMENT EXECUTE FUNCTION api.refresh_note_search_terms();
CREATE TRIGGER refresh_note_search_delete AFTER DELETE ON api.notes REFERENCING OLD TABLE AS note_search_old
  FOR EACH STATEMENT EXECUTE FUNCTION api.refresh_note_search_terms();

CREATE TYPE api.note_search_page AS (next_cursor text,has_more boolean,results jsonb);
CREATE FUNCTION api.search_notes(search_text text,after_cursor text DEFAULT NULL,page_size integer DEFAULT 20)
RETURNS SETOF api.note_search_page
LANGUAGE plpgsql STABLE SECURITY INVOKER SET search_path=pg_catalog AS $$
DECLARE
  caller text:=NULLIF(current_setting('request.jwt.claims',true)::jsonb->>'sub','');
  previous_scope text:=current_setting('gregale.note_search_scope',true);
  words text[];
  parsed tsquery;
  prefix text;
  parts text[];
  after_rank integer;
  after_id integer;
  output jsonb;
  more boolean;
  next_token text;
BEGIN
  IF caller IS NULL THEN RAISE SQLSTATE '42501' USING MESSAGE='application subject required'; END IF;
  IF search_text IS NULL OR octet_length(search_text)>1024 THEN
    RAISE SQLSTATE 'PT400' USING MESSAGE='note_search_query_invalid';
  END IF;
  words:=tsvector_to_array(to_tsvector('pg_catalog.english',search_text));
  IF cardinality(words)<1 OR cardinality(words)>16 THEN
    RAISE SQLSTATE 'PT400' USING MESSAGE='note_search_query_invalid';
  END IF;
  parsed:=plainto_tsquery('pg_catalog.english',search_text);
  IF page_size IS NULL OR page_size<1 OR page_size>100 THEN
    RAISE SQLSTATE 'PT400' USING MESSAGE='note_search_page_size_invalid';
  END IF;
  prefix:='v1.'||encode(sha256(convert_to(jsonb_build_array('gregale.note-search.v1',caller,search_text)::text,'UTF8')),'hex')||'.';
  IF after_cursor IS NOT NULL THEN
    IF octet_length(after_cursor)>128 OR left(after_cursor,length(prefix))<>prefix THEN
      RAISE SQLSTATE 'PT400' USING MESSAGE='note_search_cursor_invalid';
    END IF;
    parts:=string_to_array(substr(after_cursor,length(prefix)+1),'.');
    IF cardinality(parts)<>2 OR parts[1] !~ '^(0|[1-9][0-9]{0,6})$' OR parts[2] !~ '^[1-9][0-9]{0,9}$'
      OR parts[1] ~ '[^0-9]' OR parts[2] ~ '[^0-9]' THEN
      RAISE SQLSTATE 'PT400' USING MESSAGE='note_search_cursor_invalid';
    END IF;
    IF parts[1]::bigint>1000000 OR parts[2]::bigint>2147483647 THEN
      RAISE SQLSTATE 'PT400' USING MESSAGE='note_search_cursor_invalid';
    END IF;
    after_rank:=parts[1]::integer; after_id:=parts[2]::integer;
  END IF;
  PERFORM set_config('gregale.note_search_scope','read',true);
  WITH candidates AS MATERIALIZED (
    SELECT p.note_id FROM unnest(words) w(term)
      JOIN api.note_search_terms p ON p.subject=caller AND p.term=w.term
      GROUP BY p.note_id HAVING count(*)=cardinality(words)
  ), ranked AS MATERIALIZED (
    -- Keep the materialized ranking set narrow; fetch response bodies only
    -- for the bounded page rather than retaining every matching note body.
    SELECT n.id,
      round(ts_rank_cd(to_tsvector('pg_catalog.english',n.body),parsed,32)::numeric*1000000)::integer AS rank
    FROM candidates c JOIN api.notes n ON n.subject=caller AND n.id=c.note_id
    WHERE to_tsvector('pg_catalog.english',n.body) @@ parsed
  ), page AS MATERIALIZED (
    SELECT * FROM ranked r WHERE after_rank IS NULL OR r.rank<after_rank OR (r.rank=after_rank AND r.id<after_id)
    ORDER BY r.rank DESC,r.id DESC LIMIT page_size+1
  ), numbered AS (
    SELECT n.id,n.body,n.priority,n.created_at,n.version,p.rank,
      row_number() OVER(ORDER BY p.rank DESC,p.id DESC) AS ordinal
    FROM page p JOIN api.notes n ON n.subject=caller AND n.id=p.id
  ) SELECT COALESCE(jsonb_agg(to_jsonb(n)-'ordinal' ORDER BY n.ordinal) FILTER(WHERE n.ordinal<=page_size),'[]'::jsonb),
      count(*)>page_size,
      max(prefix||n.rank::text||'.'||n.id::text) FILTER(WHERE n.ordinal=page_size)
    INTO output,more,next_token FROM numbered n;
  PERFORM set_config('gregale.note_search_scope',COALESCE(previous_scope,''),true);
  RETURN NEXT ROW(CASE WHEN more THEN next_token ELSE NULL END,more,output)::api.note_search_page;
EXCEPTION WHEN OTHERS THEN
  PERFORM set_config('gregale.note_search_scope',COALESCE(previous_scope,''),true);
  RAISE;
END;
$$;
REVOKE ALL ON FUNCTION api.search_notes(text,text,integer) FROM PUBLIC;
COMMENT ON FUNCTION api.search_notes(text,text,integer) IS '@gregale:rpc';
