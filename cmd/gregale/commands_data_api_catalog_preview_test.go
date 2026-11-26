package main

import (
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func catalogCell(value string) *string { return &value }
func catalogResponse(rows ...[]*string) api.ManagedPostgresQueryResponse {
	columns := make([]api.ManagedPostgresQueryColumn, 0, len(dataAPICatalogColumns))
	for _, name := range dataAPICatalogColumns {
		columns = append(columns, api.ManagedPostgresQueryColumn{Name: name})
	}
	return api.ManagedPostgresQueryResponse{DatabaseID: "db", Columns: columns, Rows: rows, RowCount: len(rows)}
}
func catalogRow(name, kind, rls, force, selectable, owner, invoker, policies, total string) []*string {
	return []*string{catalogCell("preview-role"), catalogCell("f"), catalogCell("f"), catalogCell(name), catalogCell(kind), catalogCell(rls), catalogCell(force), catalogCell(selectable), catalogCell(owner), catalogCell(invoker), catalogCell(policies), catalogCell(total)}
}

func TestDataAPICatalogPreviewReportsWarningsWithoutClaimingAuthorization(t *testing.T) {
	response := catalogResponse(catalogRow("notes", "r", "f", "f", "t", "f", "f", "0", "2"), catalogRow("view", "v", "f", "f", "f", "f", "f", "0", "2"))
	report, err := parseDataAPICatalogPreview("db", response)
	if err != nil {
		t.Fatal(err)
	}
	if report.Authorization != "not_tested" || report.Role != "preview-role" || report.TotalRelations != 2 || report.Partial {
		t.Fatalf("unexpected report: %+v", report)
	}
	if got := strings.Join(report.Relations[0].Warnings, ","); !strings.Contains(got, "rls_disabled") {
		t.Fatal(got)
	}
	if got := strings.Join(report.Relations[1].Warnings, ","); !strings.Contains(got, "view_not_security_invoker") || !strings.Contains(got, "table_select_not_granted_to_preview_role") {
		t.Fatal(got)
	}
}

func TestDataAPICatalogPreviewHandlesEmptyAndTruncatedCatalogs(t *testing.T) {
	empty := catalogResponse([]*string{catalogCell("preview-role"), catalogCell("f"), catalogCell("f"), nil, nil, nil, nil, nil, nil, nil, nil, catalogCell("0")})
	if report, err := parseDataAPICatalogPreview("db", empty); err != nil || len(report.Relations) != 0 {
		t.Fatalf("empty catalog: %+v %v", report, err)
	}
	partial := catalogResponse(catalogRow("notes", "r", "t", "f", "t", "f", "f", "1", "2"))
	partial.Truncated = true
	if report, err := parseDataAPICatalogPreview("db", partial); err != nil || !report.Partial || report.TotalRelations != 2 {
		t.Fatalf("partial catalog: %+v %v", report, err)
	}
}

func TestDataAPICatalogPreviewRejectsMalformedAndInconsistentResults(t *testing.T) {
	for _, mutate := range []func(*api.ManagedPostgresQueryResponse){
		func(r *api.ManagedPostgresQueryResponse) { r.DatabaseID = "other" },
		func(r *api.ManagedPostgresQueryResponse) { r.Columns[0].Name = "other" },
		func(r *api.ManagedPostgresQueryResponse) { r.Rows[0][5] = catalogCell("maybe") },
		func(r *api.ManagedPostgresQueryResponse) { r.Rows[0][11] = catalogCell("2") },
		func(r *api.ManagedPostgresQueryResponse) { r.Rows[0][3] = catalogCell(strings.Repeat("a", 64)) },
		func(r *api.ManagedPostgresQueryResponse) { r.Truncated = true },
	} {
		r := catalogResponse(catalogRow("notes", "r", "t", "f", "t", "f", "f", "1", "1"))
		mutate(&r)
		if _, err := parseDataAPICatalogPreview("db", r); err == nil {
			t.Fatal("accepted inconsistent catalog")
		}
	}
}
