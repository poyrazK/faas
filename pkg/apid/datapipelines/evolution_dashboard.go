package datapipelines

import (
	"net/http"
	"slices"
	"strconv"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/dashboard"
	"github.com/onebox-faas/faas/pkg/datapipeline"
)

func evolutionView(p datapipeline.Pipeline) dashboard.DataPipelineEvolution {
	v := dashboard.DataPipelineEvolution{Pipeline: p.DataPipeline}
	// Domain validation is repeated on submit; these flags only describe the UI.
	v.CanPreview = datapipeline.CanEvolve(p)
	v.CanDiscard = p.Evolution != nil && p.Evolution.State == "preview" && v.CanPreview
	v.CanApprove = v.CanDiscard && p.Evolution.PreviewRevision == p.Revision && p.Evolution.Generation == p.Generation
	v.CanRetry = p.Evolution != nil && p.Evolution.State != "preview" && p.Evolution.Retry < api.DataPipelineSchemaRetriesMax && p.Reason == "relationship_api_refresh_failed"
	return v
}

func evolutionLines(s string) []string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	if s == "" {
		return nil
	}
	return strings.Split(strings.TrimSuffix(s, "\n"), "\n")
}

func (h *Handler) manageDashboardEvolution(r *http.Request, account string) (string, error) {
	revision, err := strconv.ParseInt(r.FormValue("revision"), 10, 64)
	if err != nil {
		return "", datapipeline.ErrInvalid
	}
	req := api.DataPipelineEvolutionRequest{ExpectedRevision: revision, Action: strings.TrimPrefix(r.FormValue("action"), "evolution_"), ChangeID: r.FormValue("evolution_id")}
	if req.Action == "approve" && r.FormValue("confirm_evolution") != "yes" {
		return "", datapipeline.ErrInvalid
	}
	if req.Action == "preview" {
		p, err := h.Service.Store.Get(r.Context(), account, r.FormValue("pipeline_id"))
		if err != nil {
			return "", err
		}
		if err = dashboardEvolutionInput(r, p, &req); err != nil {
			return "", err
		}
	}
	return "", h.Service.Evolve(r.Context(), account, r.FormValue("pipeline_id"), req)
}

func dashboardEvolutionInput(r *http.Request, p datapipeline.Pipeline, req *api.DataPipelineEvolutionRequest) error {
	switch r.FormValue("evolution_type") {
	case "projection":
		if p.Serving == nil || len(p.Serving.Columns) == 0 {
			return datapipeline.ErrInvalid
		}
		req.Kind = "projection"
		req.Columns = map[string][]string{}
		for _, table := range p.Tables {
			req.Columns[table] = slices.Clone(p.Serving.Columns[table])
		}
		req.Columns[r.FormValue("table")] = evolutionLines(r.FormValue("columns"))
	case "auth_mapping_add", "auth_mapping_remove":
		req.Kind = "auth_organizations"
		a := api.DataPipelineAuthOrganizations{AuthAppID: r.FormValue("auth_app_id")}
		if p.Serving != nil && p.Serving.AuthOrganizations != nil {
			a = *p.Serving.AuthOrganizations
			a.Mappings = slices.Clone(a.Mappings)
			if app := r.FormValue("auth_app_id"); app != "" {
				a.AuthAppID = app
			}
		}
		org := r.FormValue("organization_id")
		a.Mappings = slices.DeleteFunc(a.Mappings, func(m api.DataPipelineOrganizationTenant) bool { return m.OrganizationID == org })
		if r.FormValue("evolution_type") == "auth_mapping_add" {
			a.Mappings = append(a.Mappings, api.DataPipelineOrganizationTenant{OrganizationID: org, Tenant: r.FormValue("tenant_value")})
		}
		req.AuthOrganizations = &a
	case "relationship_add", "relationship_remove":
		req.Kind = "relationships"
		if p.Serving != nil {
			req.Relationships = slices.Clone(p.Serving.Relationships)
		}
		name := r.FormValue("relationship_name")
		req.Relationships = slices.DeleteFunc(req.Relationships, func(v api.DataPipelineRelationship) bool { return v.Name == name })
		if r.FormValue("evolution_type") == "relationship_add" {
			req.Relationships = append(req.Relationships, api.DataPipelineRelationship{Name: name, SourceTable: r.FormValue("source_table"), TargetTable: r.FormValue("target_table"), SourceColumns: evolutionLines(r.FormValue("source_columns")), TargetColumns: evolutionLines(r.FormValue("target_columns"))})
		}
	default:
		return datapipeline.ErrInvalid
	}
	return nil
}
