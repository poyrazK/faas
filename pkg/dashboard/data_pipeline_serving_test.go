package dashboard

import (
	"log/slog"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestDataPipelineServingControlsEscapeMemberships(t *testing.T) {
	data := DataPipelinesData{Enabled: true, ReadersEnabled: true, SetupEnabled: true, MembershipLimit: api.DataPipelineTenantMembershipsMax,
		Check:   &api.DataConnectionCheck{Fresh: true, State: "passed", Capacity: &api.DataPipelineCapacity{Source: &api.DataEndpointCapacity{State: "ready"}}, Tables: []api.DataSourceTable{{Name: "orders", ColumnSelectionSupported: true, Columns: []api.DataSourceColumn{{Name: "id", PrimaryKey: true}, {Name: "tenant_id", Type: "text"}, {Name: "secret", Unsupported: true}}}}},
		Readers: []DataPipelineReaders{{PipelineID: "pipeline", Tenant: true, TenantColumn: "tenant_id", CanRequest: true, Memberships: []api.DataPipelineTenantMembership{{Subject: `reader" onfocus="alert(1)`, Tenant: "<script>tenant</script>"}}}}}
	out := httptest.NewRecorder()
	if err := Render(out, slog.Default(), "nonce", Page{Body: "data_pipelines", Data: data}); err != nil {
		t.Fatal(err)
	}
	body := out.Body.String()
	for _, want := range []string{`name="access_mode"`, `name="column_mode"`, `name="columns.orders"`, `name="tenant_mode" value="yes"`, `data-member-limit="256"`, "&lt;script&gt;tenant&lt;/script&gt;"} {
		if !strings.Contains(body, want) {
			t.Errorf("missing serving control %q", want)
		}
	}
	if strings.Contains(body, "<script>tenant") || strings.Contains(body, `value="reader" onfocus=`) {
		t.Fatal("membership escaped its HTML attribute")
	}
}
