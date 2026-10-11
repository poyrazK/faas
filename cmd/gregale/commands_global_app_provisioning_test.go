package main

// adr: 797
import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

func TestGlobalAppProvisionCLIEnvironmentCredentials(t *testing.T) {
	key, _, err := api.GenerateAPIKey()
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("GREGALE_REGION_KEY", key)
	path := filepath.Join(t.TempDir(), "regions.json")
	raw, _ := json.Marshal(map[string]any{"targets": []map[string]any{{"region": "eu-fsn", "regional_account_id": uuid.NewString(), "regional_token_env": "GREGALE_REGION_KEY", "app": map[string]any{"ram_mb": 128}}}})
	if err := os.WriteFile(path, raw, 0600); err != nil {
		t.Fatal(err)
	}
	var calls int
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Method != http.MethodPut || r.URL.Path != "/v1/global-apps/provision-shop/provisioning" || r.Header.Get("Authorization") != "Bearer directory-key" {
			t.Error("incorrect request")
		}
		var req api.PutGlobalAppProvisioningRequest
		if json.NewDecoder(r.Body).Decode(&req) != nil || len(req.Targets) != 1 || req.Targets[0].RegionalAPIKey != key {
			t.Error("incorrect regional intent")
		}
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte(`[{"region":"eu-fsn","status":"attached","provision_id":"test"}]`))
	}))
	defer s.Close()
	t.Setenv("FAAS_API", s.URL)
	t.Setenv("FAAS_TOKEN", "directory-key")
	out, errout, restore := swapIO(t)
	defer restore()
	if code := cmdGlobalApp([]string{"provision", "provision-shop", "--targets", path}); code != 0 || calls != 1 {
		t.Fatalf("exit=%d calls=%d", code, calls)
	}
	if strings.Contains(out.String()+errout(), key) {
		t.Fatal("regional credential leaked")
	}
	if err := os.WriteFile(path, []byte(`{"targets":[],"regional_api_key":"plaintext"}`), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := readProvisionTargets(path); err == nil {
		t.Fatal("plaintext key in target file accepted")
	}
}
