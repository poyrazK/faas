// adr: 592 — portable reader permissions and capability discovery.
package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestCmdPostgresCapabilitiesUsesSafeRegionalContract(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/v1/postgres/capabilities" || r.URL.Query().Get("region") != "eu" {
			t.Errorf("unexpected capability request: %s %s", r.Method, r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"contract_version":1,"region":"eu","provisioning_enabled":false,"database_limit":1,"postgres_majors":[17],"service_classes":["development"],"availability":["single_zone"],"credential_access":["read_only","read_write"],"scale_to_zero":true,"always_on":false,"pooled_connections":true,"point_in_time_restore":true,"storage_limit_bytes":10737418240,"restore_window_seconds":604800,"password":"PRIVATE_PASSWORD","backend_id":"PRIVATE_BACKEND"}`))
	}))
	defer srv.Close()
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_live_test")
	previousOut, previousJSON := osStdout, jsonOutput
	t.Cleanup(func() { osStdout, jsonOutput = previousOut, previousJSON })
	for _, asJSON := range []bool{true, false} {
		var out bytes.Buffer
		osStdout, jsonOutput = &out, asJSON
		if code := cmdPostgres([]string{"capabilities", "--region", "eu"}); code != 0 {
			t.Fatal("capability command failed", code)
		}
		if strings.Contains(out.String(), "PRIVATE_") {
			t.Fatal("capability command leaked provider material")
		}
		if asJSON {
			var view api.ManagedPostgresCapabilities
			if err := json.Unmarshal(out.Bytes(), &view); err != nil || view.ContractVersion != 1 || view.DatabaseLimit != 1 || view.ProvisioningEnabled {
				t.Fatal("invalid capability JSON", err)
			}
		} else if !strings.Contains(out.String(), "read_only") || !strings.Contains(out.String(), "provisioning_enabled: false") {
			t.Fatal("human capability output omitted access or rollout status")
		}
	}
}
