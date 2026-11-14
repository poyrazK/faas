package datapipelines

import (
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/datapipeline"
)

func dashboardMemberships(r *http.Request) ([]api.DataPipelineTenantMembership, error) {
	subjects, tenants := r.Form["tenant_subject"], r.Form["tenant_value"]
	if len(subjects) != len(tenants) || len(subjects) > api.DataPipelineTenantMembershipsMax+1 {
		return nil, datapipeline.ErrInvalid
	}
	members := []api.DataPipelineTenantMembership{}
	for i, subject := range subjects {
		if subject == "" && tenants[i] == "" {
			continue
		}
		members = append(members, api.DataPipelineTenantMembership{Subject: subject, Tenant: tenants[i]})
	}
	return members, nil
}

func dashboardServing(r *http.Request) (*api.DataPipelineServing, error) {
	var s api.DataPipelineServing
	if r.FormValue("column_mode") == "selected" {
		s.Columns = map[string][]string{}
		for _, table := range r.Form["tables"] {
			s.Columns[table] = r.Form["columns."+table]
		}
	} else if mode := r.FormValue("column_mode"); mode != "" && mode != "all" {
		return nil, datapipeline.ErrInvalid
	}
	if r.FormValue("access_mode") == "tenant" {
		members, err := dashboardMemberships(r)
		if err != nil {
			return nil, err
		}
		s.TenantAccess = &api.DataPipelineTenantAccess{Column: r.FormValue("tenant_column"), Memberships: members}
	} else if mode := r.FormValue("access_mode"); mode != "" && mode != "trusted" {
		return nil, datapipeline.ErrInvalid
	}
	if s.Columns == nil && s.TenantAccess == nil {
		return nil, nil
	}
	s.Version = 1
	return &s, nil
}
