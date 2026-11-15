package datapipeline

import (
	"context"
	"slices"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
)

// ReadAuthOrganizationMembers runs only on the worker's privately resolved Auth
// endpoint. It reads customer data, never the control catalog (ADR-20261113205500).
func ReadAuthOrganizationMembers(ctx context.Context, conn *pgx.Conn, a api.DataPipelineAuthOrganizations) ([]api.DataPipelineTenantMembership, error) {
	orgs := make([]string, 0, len(a.Mappings))
	tenants := map[string]string{}
	for _, m := range a.Mappings {
		orgs = append(orgs, m.OrganizationID)
		tenants[m.OrganizationID] = m.Tenant
	}
	// One statement observes membership and user lifecycle consistently.
	rows, err := conn.Query(ctx, `SELECT m.org_id::text,m.user_id::text FROM gregale_auth.memberships m JOIN auth.users u ON u.id=m.user_id
 WHERE m.org_id=ANY($1::uuid[]) AND u.deleted_at IS NULL AND (u.banned_until IS NULL OR u.banned_until<=now())
 ORDER BY m.org_id,m.user_id LIMIT $2`, orgs, api.DataPipelineTenantMembershipsMax+1)
	if err != nil {
		return nil, ErrUnavailable
	}
	defer rows.Close()
	members := []api.DataPipelineTenantMembership{}
	for rows.Next() {
		var org, user string
		if err = rows.Scan(&org, &user); err != nil {
			return nil, ErrUnavailable
		}
		members = append(members, api.DataPipelineTenantMembership{Subject: user, Tenant: tenants[org]})
	}
	if rows.Err() != nil || len(members) > api.DataPipelineTenantMembershipsMax {
		return nil, ErrUnavailable
	}
	slices.SortFunc(members, compareMemberships)
	return slices.Compact(members), nil
}
