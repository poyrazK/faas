-- Business-data aggregates retain current membership and row visibility
-- (ADR-1678). One statement snapshot, integer cents, no cached/global totals.
CREATE TYPE api.team_order_summary AS (
  tenant_id text,
  order_count text,
  total_cents text,
  by_status jsonb
);

CREATE FUNCTION api.summarize_team_orders(team_id text,
  status_filter api.order_status DEFAULT NULL,
  minimum_total integer DEFAULT NULL,maximum_total integer DEFAULT NULL)
RETURNS SETOF api.team_order_summary
LANGUAGE plpgsql STABLE SECURITY INVOKER SET search_path=pg_catalog AS $$
DECLARE actor text:=NULLIF(current_setting('request.jwt.claims',true)::jsonb->>'sub','');
BEGIN
  IF team_id IS NULL OR team_id='' OR minimum_total<0 OR maximum_total<0 OR minimum_total>maximum_total THEN
    RAISE SQLSTATE 'PT400' USING MESSAGE='invalid_order_summary';
  END IF;
  -- JWT tenant hints never grant team access. Check the authoritative row
  -- explicitly, even if another approved RPC has a membership-read scope.
  IF actor IS NULL OR NOT EXISTS(SELECT 1 FROM api.team_members m WHERE m.tenant_id=team_id AND m.subject=actor) THEN
    RAISE SQLSTATE '42501' USING MESSAGE='team_membership_required';
  END IF;
  RETURN QUERY
    WITH buckets AS MATERIALIZED (
      SELECT o.status,count(*) AS order_count,
        COALESCE(sum(o.total_cents::numeric),0) AS total_cents,grouping(o.status) AS overall
      FROM api.team_orders o WHERE o.tenant_id=team_id
        AND (status_filter IS NULL OR o.status=status_filter)
        AND (minimum_total IS NULL OR o.total_cents>=minimum_total)
        AND (maximum_total IS NULL OR o.total_cents<=maximum_total)
      GROUP BY GROUPING SETS((o.status),())
    ), statuses AS (
      SELECT s.status,s.ordinality,COALESCE(b.order_count,0)::text AS order_count,
        COALESCE(b.total_cents,0)::text AS total_cents
      FROM unnest(enum_range(NULL::api.order_status)) WITH ORDINALITY s(status,ordinality)
      LEFT JOIN buckets b ON b.overall=0 AND b.status=s.status
    )
    SELECT team_id,b.order_count::text,b.total_cents::text,
      (SELECT jsonb_agg(jsonb_build_object('status',s.status,'order_count',s.order_count,'total_cents',s.total_cents)
        ORDER BY s.ordinality) FROM statuses s)
    FROM buckets b WHERE b.overall=1;
END;
$$;
REVOKE ALL ON FUNCTION api.summarize_team_orders(text,api.order_status,integer,integer) FROM PUBLIC;
COMMENT ON FUNCTION api.summarize_team_orders(text,api.order_status,integer,integer) IS '@gregale:rpc';
