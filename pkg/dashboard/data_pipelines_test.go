package dashboard

import (
	"io"
	"log/slog"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestDataPipelinesRenderCompatibilityAndFreshness(t *testing.T) {
	data := DataPipelinesData{Enabled: true, CSRFToken: "bound-token", ConnectionID: "source", ConnectionRevision: 2,
		Check: &api.DataConnectionCheck{ConnectionID: "source", DatabaseID: "mirror", State: "passed", Fresh: true, Capacity: &api.DataPipelineCapacity{State: "ready"}, Tables: []api.DataSourceTable{
			{Name: "orders", Supported: true, Columns: []api.DataSourceColumn{{Name: "id", Type: "int8", PrimaryKey: true}}},
			{Name: "<script>secret</script>", Issues: []api.DataConnectionIssue{{Message: "Unsupported column", Column: "<img onerror=alert(1)>"}}},
		}}, Pipelines: []api.DataPipeline{{Name: "orders", State: "live", Tables: []string{"orders"}, TablesReady: 1, ObservationFresh: false, AppliedThroughAt: "2026-10-17T08:00:00Z", Health: &api.DataPipelineHealth{Message: "Monitoring is stale", Recovery: "Check worker availability"}}}}
	data.Readers = []DataPipelineReaders{{PipelineID: "pipeline", Name: "orders", ID: "reader-preview", Revision: 2, State: "preview", Current: []string{"alice"}, Added: []string{"<script>reader</script>"}, Removed: []string{"alice"}, Tables: []string{"orders"}, CanApprove: true, CanDiscard: true}}
	data.Verifications = []DataPipelineVerificationItem{{PipelineID: "pipeline", ID: "run-id", Name: "large <orders>", State: "running", Phase: "Backfilling and verifying new columns", Table: "<script>key</script>", Rows: 12000, BackfilledRows: 8000, Ranges: 6, DirtyRanges: 1, TablesTotal: 1, CanCancel: true}}
	data.TableSelections = []DataPipelineTableSelection{{PipelineID: "pipeline", ConnectionID: "source", Name: "orders", Revision: 2, Tables: []string{"orders"}, Choices: []string{"customers", "<script>choice</script>"}, CanRequest: true}}
	response := httptest.NewRecorder()
	err := Render(response, slog.New(slog.NewTextHandler(io.Discard, nil)), "test-nonce", Page{Body: "data_pipelines", Data: data})
	if err != nil {
		t.Fatal(err)
	}
	body := response.Body.String()
	for _, want := range []string{"Preview table additions", "Existing tables stay readable", "&lt;script&gt;choice&lt;/script&gt;", "&lt;script&gt;reader&lt;/script&gt;", "confirm_readers", "reader-preview", "Apply reader changes", "every row in every selected table"} {
		if !strings.Contains(body, want) {
			t.Errorf("table onboarding missing %q", want)
		}
	}
	for _, want := range []string{"Start pipeline", "Data verification", "12000 rows checked", "8000 rows updated in approved new columns", "Stop verification", "confirm_verification", "Applied through checkpoint", "Monitoring is stale", "Check worker availability", "1/1 tables ready", "Stale", "bound-token", "read every row", `name="database_id" value="mirror"`, `nonce="test-nonce"`} {
		if !strings.Contains(body, want) {
			t.Errorf("missing %q", want)
		}
	}
	if strings.Contains(body, "<script>secret") || strings.Contains(body, "<img onerror") || strings.Contains(body, "<script>reader") {
		t.Fatal("source metadata escaped HTML encoding")
	}
	data.Check.Capacity.State = "needs_qualification"
	response = httptest.NewRecorder()
	if err = Render(response, slog.Default(), "", Page{Body: "data_pipelines", Data: data}); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(response.Body.String(), ">Start pipeline</button>") {
		t.Fatal("unknown capacity enabled pipeline creation")
	}
	data.Check.Capacity.State = "ready"
	data.Check.Fresh = false
	response = httptest.NewRecorder()
	if err = Render(response, slog.Default(), "", Page{Body: "data_pipelines", Data: data}); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(response.Body.String(), ">Start pipeline</button>") {
		t.Fatal("expired check enabled pipeline creation")
	}
}

func TestDataPipelineSavedSetupRenderAllowsNewMirrorAndRetainsRecovery(t *testing.T) {
	data := DataPipelinesData{Enabled: true, SetupEnabled: true, CSRFToken: "bound-token", ConnectionID: "source", ConnectionRevision: 2,
		Check:  &api.DataConnectionCheck{ConnectionID: "source", State: "passed", Fresh: true, Capacity: &api.DataPipelineCapacity{State: "needs_qualification", Source: &api.DataEndpointCapacity{State: "ready"}}, Tables: []api.DataSourceTable{{Name: "orders", Supported: true}}},
		Setups: []DataPipelineSetupItem{{ID: "saved-id", Name: "<saved-orders>", State: "blocked", Phase: "provisioning", Progress: "Preparing your managed mirror", Remediation: "Retry with the same mirror", DatabaseID: "stable-mirror", Revision: 2, Tables: []string{"orders"}}, {Name: "ready-orders", State: "ready", DatabaseID: "ready-mirror"}}}
	out := httptest.NewRecorder()
	if err := Render(out, slog.Default(), "test-nonce", Page{Body: "data_pipelines", Data: data}); err != nil {
		t.Fatal(err)
	}
	body := out.Body.String()
	for _, want := range []string{"Start saved setup", "billed under your account", "Retry saved setup", "stable-mirror", "--database ready-mirror --read-only --schema public --auth AUTH_APP", "Rotate source credential", "confirm_cancel", "revision\" value=\"2", "&lt;saved-orders&gt;"} {
		if !strings.Contains(body, want) {
			t.Errorf("missing %q", want)
		}
	}
	if strings.Contains(body, "<saved-orders>") {
		t.Fatal("setup metadata bypassed HTML encoding")
	}
}

func TestDataPipelineDataAPISetupRenderProgressAndAuthenticatedExample(t *testing.T) {
	data := DataPipelinesData{Enabled: true, SetupEnabled: true, DataAPIEnabled: true, AuthApps: []DataPipelineAuthApp{{ID: "auth-id", Name: "shop-auth"}}, Check: &api.DataConnectionCheck{ConnectionID: "source", Fresh: true, State: "passed", Tables: []api.DataSourceTable{{Name: "orders", Supported: true}}, Capacity: &api.DataPipelineCapacity{Source: &api.DataEndpointCapacity{State: "ready"}}}, Setups: []DataPipelineSetupItem{{Name: "orders", State: "working", Phase: "waiting_api", DataAPIName: "orders-api", DeploymentID: "deployment", Progress: "Building and deploying the Data API", Tables: []string{"orders"}}}}
	render := func() string {
		out := httptest.NewRecorder()
		if err := Render(out, slog.Default(), "nonce", Page{Body: "data_pipelines", Data: data}); err != nil {
			t.Fatal(err)
		}
		return out.Body.String()
	}
	body := render()
	for _, want := range []string{`name="data_api_name"`, `name="auth_app_id"`, `value="auth-id"`, "shop-auth", "app quota and compute billing", "/dashboard/apps/orders-api/deployments/deployment"} {
		if !strings.Contains(body, want) {
			t.Errorf("missing %q", want)
		}
	}
	if strings.Contains(body, "curl --get") {
		t.Fatal("query example appeared before readiness")
	}
	data.Setups[0].State = "ready"
	data.Setups[0].Endpoint = "https://orders-api.example"
	body = render()
	for _, want := range []string{"https://orders-api.example/rest/v1/orders", "limit=10", "Authorization: Bearer $GREGALE_USER_JWT"} {
		if !strings.Contains(body, want) {
			t.Errorf("ready example missing %q", want)
		}
	}
	if strings.Contains(body, "gregale data-api create DATA_API_NAME") {
		t.Fatal("ready API still asks for CLI deployment")
	}
}
