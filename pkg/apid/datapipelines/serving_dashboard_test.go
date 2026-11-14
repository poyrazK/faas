package datapipelines

import (
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestDashboardServingPreservesOpaqueMembershipAndColumnNames(t *testing.T) {
	form := url.Values{"tables": {"orders"}, "column_mode": {"selected"}, "columns.orders": {"id", "Tenant Key"}, "access_mode": {"tenant"}, "tenant_column": {"Tenant Key"}, "tenant_subject": {" user ", ""}, "tenant_value": {"tenant,one", ""}}
	r := httptest.NewRequest("POST", "/dashboard/data", strings.NewReader(form.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if err := r.ParseForm(); err != nil {
		t.Fatal(err)
	}
	s, err := dashboardServing(r)
	if err != nil || s.Version != 1 || s.TenantAccess.Memberships[0].Subject != " user " || s.TenantAccess.Memberships[0].Tenant != "tenant,one" || len(s.Columns["orders"]) != 2 {
		t.Fatal("form changed authoritative values", s, err)
	}
	r.Form["tenant_value"] = nil
	if _, err = dashboardServing(r); err == nil {
		t.Fatal("mismatched membership form accepted")
	}
}
