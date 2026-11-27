package api

import (
	"context"
	"net/http"
	"net/url"
)

// DataAPICatalogPreviewSQL is fixed platform SQL. It reads catalog metadata
// only; it never reads customer relation rows or policy expressions. The
// provider executes it in a bounded read-only transaction.
const DataAPICatalogPreviewSQL = `WITH relations AS (
 SELECT c.oid, c.relname, c.relkind, c.relrowsecurity, c.relforcerowsecurity, c.relowner, c.reloptions,
        pg_catalog.count(*) OVER () AS total_relations
 FROM pg_catalog.pg_class c JOIN pg_catalog.pg_namespace n ON n.oid OPERATOR(pg_catalog.=) c.relnamespace
 WHERE n.nspname OPERATOR(pg_catalog.=) 'public' AND
       (c.relkind OPERATOR(pg_catalog.=) 'r' OR c.relkind OPERATOR(pg_catalog.=) 'p' OR c.relkind OPERATOR(pg_catalog.=) 'v' OR c.relkind OPERATOR(pg_catalog.=) 'm' OR c.relkind OPERATOR(pg_catalog.=) 'f')
)
SELECT current_user AS inspected_role, r.rolsuper AS superuser, r.rolbypassrls AS bypass_rls,
 c.relname AS relation, c.relkind AS kind, c.relrowsecurity AS rls,
 c.relforcerowsecurity AS force_rls,
 CASE WHEN c.oid IS NULL THEN NULL ELSE pg_catalog.has_table_privilege(current_user,c.oid,'SELECT') END AS can_select,
 CASE WHEN c.oid IS NULL THEN NULL ELSE pg_catalog.pg_has_role(current_user,c.relowner,'MEMBER') END AS owner_member,
 CASE WHEN c.oid IS NULL THEN NULL ELSE COALESCE((SELECT (pg_catalog.lower(o.option_value) OPERATOR(pg_catalog.=) 'true' OR pg_catalog.lower(o.option_value) OPERATOR(pg_catalog.=) 'on' OR pg_catalog.lower(o.option_value) OPERATOR(pg_catalog.=) 'yes' OR pg_catalog.lower(o.option_value) OPERATOR(pg_catalog.=) '1') FROM pg_catalog.pg_options_to_table(c.reloptions) o WHERE o.option_name OPERATOR(pg_catalog.=) 'security_invoker'),false) END AS security_invoker,
 CASE WHEN c.oid IS NULL THEN NULL ELSE (SELECT pg_catalog.count(*) FROM pg_catalog.pg_policy p WHERE p.polrelid OPERATOR(pg_catalog.=) c.oid) END AS policies,
 COALESCE(c.total_relations,0) AS total_relations
FROM pg_catalog.pg_roles r LEFT JOIN relations c ON true
WHERE r.rolname OPERATOR(pg_catalog.=) current_user ORDER BY c.relname LIMIT 1001`

// PreviewDataAPICatalog runs the fixed read-only catalog inspection. No SQL
// or arbitrary database command is accepted by this endpoint.
func (c *Client) PreviewDataAPICatalog(ctx context.Context, databaseID string) (ManagedPostgresQueryResponse, error) {
	var out ManagedPostgresQueryResponse
	err := c.do(ctx, http.MethodPost, "/v1/postgres/databases/"+url.PathEscape(databaseID)+"/data-api-catalog-preview", nil, &out)
	return out, err
}
