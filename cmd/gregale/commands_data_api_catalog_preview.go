package main

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"unicode/utf8"

	"github.com/onebox-faas/faas/pkg/api"
)

type dataAPICatalogRelation struct {
	Name            string   `json:"name"`
	Kind            string   `json:"kind"`
	RLS             bool     `json:"rls"`
	ForceRLS        bool     `json:"force_rls"`
	TableSelect     bool     `json:"table_select"`
	OwnerMember     bool     `json:"owner_member"`
	SecurityInvoker bool     `json:"security_invoker"`
	PolicyCount     int64    `json:"policy_count"`
	Warnings        []string `json:"warnings"`
}
type dataAPICatalogPreview struct {
	Version        int                      `json:"version"`
	DatabaseID     string                   `json:"database_id"`
	Schema         string                   `json:"schema"`
	Role           string                   `json:"preview_role"`
	Superuser      bool                     `json:"superuser"`
	BypassRLS      bool                     `json:"bypass_rls"`
	TotalRelations int64                    `json:"total_relations"`
	Partial        bool                     `json:"partial"`
	Relations      []dataAPICatalogRelation `json:"relations"`
	Authorization  string                   `json:"authorization"`
	Notes          []string                 `json:"notes"`
}

var dataAPICatalogColumns = []string{"inspected_role", "superuser", "bypass_rls", "relation", "kind", "rls", "force_rls", "can_select", "owner_member", "security_invoker", "policies", "total_relations"}

func parseDataAPICatalogPreview(databaseID string, response api.ManagedPostgresQueryResponse) (dataAPICatalogPreview, error) {
	report := dataAPICatalogPreview{Version: 1, DatabaseID: databaseID, Schema: "public", Partial: response.Truncated, Relations: []dataAPICatalogRelation{}, Authorization: "not_tested", Notes: []string{"Preview may create or refresh a persistent provider-managed read-only login and wake the database; it creates no app or binding.", "Grants and role flags describe the provider-managed preview identity, not the future API binding.", "A table-level SELECT result does not include column-only grants.", "Catalog metadata does not establish effective policy behavior or two-user isolation."}}
	invalid := func() (dataAPICatalogPreview, error) {
		return dataAPICatalogPreview{}, errors.New("invalid or inconsistent catalog preview response")
	}
	if response.DatabaseID != databaseID || len(response.Columns) != len(dataAPICatalogColumns) || response.RowCount != len(response.Rows) || len(response.Rows) == 0 || len(response.Rows) > 1000 {
		return invalid()
	}
	for i, name := range dataAPICatalogColumns {
		if response.Columns[i].Name != name {
			return invalid()
		}
	}
	seen := map[string]bool{}
	for i, row := range response.Rows {
		if len(row) != len(dataAPICatalogColumns) || row[0] == nil || row[1] == nil || row[2] == nil || row[11] == nil {
			return invalid()
		}
		role := *row[0]
		if role == "" || len(role) > 63 || !utf8.ValidString(role) {
			return invalid()
		}
		super, err := strconv.ParseBool(*row[1])
		if err != nil {
			return invalid()
		}
		bypass, err := strconv.ParseBool(*row[2])
		if err != nil {
			return invalid()
		}
		total, err := strconv.ParseInt(*row[11], 10, 64)
		if err != nil || total < 0 {
			return invalid()
		}
		if i == 0 {
			report.Role, report.Superuser, report.BypassRLS, report.TotalRelations = role, super, bypass, total
		} else if report.Role != role || report.Superuser != super || report.BypassRLS != bypass || report.TotalRelations != total {
			return invalid()
		}
		if row[3] == nil {
			if len(response.Rows) != 1 || total != 0 || response.Truncated {
				return invalid()
			}
			for _, v := range row[4:11] {
				if v != nil {
					return invalid()
				}
			}
			continue
		}
		for _, v := range row[4:11] {
			if v == nil {
				return invalid()
			}
		}
		name, kind := *row[3], *row[4]
		if name == "" || len(name) > 63 || !utf8.ValidString(name) || seen[name] || (kind != "r" && kind != "p" && kind != "v" && kind != "m" && kind != "f") {
			return invalid()
		}
		seen[name] = true
		values := make([]bool, 5)
		for j, index := range []int{5, 6, 7, 8, 9} {
			values[j], err = strconv.ParseBool(*row[index])
			if err != nil {
				return invalid()
			}
		}
		policies, err := strconv.ParseInt(*row[10], 10, 64)
		if err != nil || policies < 0 {
			return invalid()
		}
		relation := dataAPICatalogRelation{Name: name, Kind: kind, RLS: values[0], ForceRLS: values[1], TableSelect: values[2], OwnerMember: values[3], SecurityInvoker: values[4], PolicyCount: policies, Warnings: []string{}}
		if !relation.TableSelect {
			relation.Warnings = append(relation.Warnings, "table_select_not_granted_to_preview_role")
		}
		if (kind == "r" || kind == "p") && !relation.RLS {
			relation.Warnings = append(relation.Warnings, "rls_disabled")
		}
		if (kind == "r" || kind == "p") && relation.RLS && policies == 0 {
			relation.Warnings = append(relation.Warnings, "no_rls_policies")
		}
		if kind == "v" && !relation.SecurityInvoker {
			relation.Warnings = append(relation.Warnings, "view_not_security_invoker")
		}
		if kind == "m" || kind == "f" {
			relation.Warnings = append(relation.Warnings, "relation_requires_separate_review")
		}
		report.Relations = append(report.Relations, relation)
	}
	if report.TotalRelations < int64(len(report.Relations)) || (report.TotalRelations > int64(len(report.Relations)) && !report.Partial) || (report.Partial && report.TotalRelations <= int64(len(report.Relations))) {
		return invalid()
	}
	return report, nil
}

func readDataAPICatalogPreview(ctx context.Context, client *api.Client, database string) (dataAPICatalogPreview, error) {
	db, err := resolveManagedPostgresDatabase(ctx, client, database)
	if err != nil {
		return dataAPICatalogPreview{}, err
	}
	if db.State != "ready" || db.Hold != "" {
		return dataAPICatalogPreview{}, errors.New("the managed database must be ready and not held")
	}
	result, err := client.PreviewDataAPICatalog(ctx, db.ID)
	if routeNotOffered(err) {
		result, err = client.QueryManagedPostgresDatabase(ctx, db.ID, api.ManagedPostgresQueryRequest{SQL: api.DataAPICatalogPreviewSQL, MaxRows: 1000})
	}
	if err != nil {
		return dataAPICatalogPreview{}, fmt.Errorf("%w; if this backend does not support managed preview, use --inspect-app with an existing usable binding", err)
	}
	return parseDataAPICatalogPreview(db.ID, result)
}

func previewDataAPICatalog(ctx context.Context, client *api.Client, database string) int {
	if !jsonOutput {
		_, _ = fmt.Fprintln(osStdout, "Preview may create or refresh a persistent provider-managed read-only login and wake the database; no app or binding is created.")
	}
	report, err := readDataAPICatalogPreview(ctx, client, database)
	if err != nil {
		return printErr("Could not interpret catalog preview", err)
	}
	if jsonOutput {
		if err := writeJSON(report); err != nil {
			return printErr("Could not write catalog preview", err)
		}
	} else {
		_, _ = fmt.Fprintf(osStdout, "Public-schema preview using read-only role %q (superuser=%t, bypass_rls=%t).\n", report.Role, report.Superuser, report.BypassRLS)
		for _, relation := range report.Relations {
			_, _ = fmt.Fprintf(osStdout, "  %q kind=%s rls=%t force_rls=%t table_select=%t owner_member=%t security_invoker=%t policies=%d", relation.Name, relation.Kind, relation.RLS, relation.ForceRLS, relation.TableSelect, relation.OwnerMember, relation.SecurityInvoker, relation.PolicyCount)
			for _, warning := range relation.Warnings {
				_, _ = fmt.Fprintf(osStdout, " warning=%s", warning)
			}
			_, _ = fmt.Fprintln(osStdout)
		}
		_, _ = fmt.Fprintf(osStdout, "Showing %d of %d relations; authorization has not been tested.\n", len(report.Relations), report.TotalRelations)
		for _, note := range report.Notes {
			_, _ = fmt.Fprintln(osStdout, note)
		}
		if report.Partial {
			_, _ = fmt.Fprintln(osStderr, "Preview is incomplete because the catalog output limit was reached.")
		}
	}
	if report.Partial {
		return 1
	}
	return 0
}
