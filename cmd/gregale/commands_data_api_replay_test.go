package main

import (
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func dataAPIReplayJWT(subject string) string {
	claims, _ := json.Marshal(map[string]string{"sub": subject})
	return "application." + base64.RawURLEncoding.EncodeToString(claims) + ".signature"
}
func dataAPIHostedReplayFixture(t *testing.T, root string) (string, string) {
	t.Helper()
	collection, identities := filepath.Join(root, "requests.json"), filepath.Join(root, "identities.json")
	content := `{"version":2,"scenarios":[{"name":"isolation","requests":[{"name":"Alice reads","method":"GET","path":"/rest/v1/notes","identity":"alice","expect":{"status":200,"json":[{"id":1}]}},{"name":"Bob denied Alice row","method":"GET","path":"/rest/v1/notes","identity":"bob","expect":{"status":200,"json":[]}}]}]}`
	if err := os.WriteFile(collection, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(identities, []byte(`{"alice":"GREGALE_REPLAY_ALICE_JWT","bob":"GREGALE_REPLAY_BOB_JWT"}`), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GREGALE_REPLAY_ALICE_JWT", dataAPIReplayJWT("alice"))
	t.Setenv("GREGALE_REPLAY_BOB_JWT", dataAPIReplayJWT("bob"))
	return collection, identities
}
func TestDataAPIHostedReplayCLI(t *testing.T) {
	for _, failure := range []string{"", "assertion", "unknown-scenario", "duplicate-subject", "account-key", "redirect", "deadline"} {
		t.Run(failure, func(t *testing.T) {
			root := t.TempDir()
			collection, identities := dataAPIHostedReplayFixture(t, root)
			t.Setenv("GREGALE_DATA_API_REFRESH_TOKEN", "")
			if failure == "duplicate-subject" {
				t.Setenv("GREGALE_REPLAY_BOB_JWT", dataAPIReplayJWT("alice"))
			}
			if failure == "account-key" {
				t.Setenv("GREGALE_REPLAY_BOB_JWT", "owner-test-token")
			}
			calls := 0
			var redirected atomic.Int64
			trap := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { redirected.Add(1); w.WriteHeader(http.StatusOK) }))
			defer trap.Close()
			serving := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != "GET" {
					t.Error("unexpected replay mutation")
				}
				switch r.URL.Path {
				case "/openapi.json":
					w.WriteHeader(http.StatusUnauthorized)
				case "/__gregale/schema":
					if failure == "redirect" {
						w.Header().Set("Location", trap.URL)
						w.WriteHeader(http.StatusFound)
						return
					}
					writeJSONTest(w, map[string]any{"version": 1, "ready": true, "fingerprint": strings.Repeat("f", 64)})
				case "/rest/v1/notes":
					if r.Header.Get("Authorization") == "Bearer "+dataAPIReplayJWT("alice") || failure == "assertion" {
						writeJSONTest(w, []map[string]int{{"id": 1}})
					} else {
						writeJSONTest(w, []map[string]int{})
					}
				default:
					t.Errorf("unexpected replay route %s", r.URL.Path)
				}
			}))
			defer serving.Close()
			certificate := filepath.Join(root, "ca.pem")
			if err := os.WriteFile(certificate, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: serving.Certificate().Raw}), 0600); err != nil {
				t.Fatal(err)
			}
			t.Setenv("NODE_EXTRA_CA_CERTS", certificate)
			management := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				if r.Method != "GET" || r.URL.Path != "/v1/apps/notes" {
					t.Error("unexpected management action")
				}
				writeJSONTest(w, api.AppResponse{Slug: "notes", URL: serving.URL})
			}))
			defer management.Close()
			t.Setenv("FAAS_API", management.URL)
			t.Setenv("FAAS_TOKEN", "owner-test-token")
			stdout, stderr, restore := swapIO(t)
			defer restore()
			args := []string{"--json", "data-api", "replay", "notes", "--collection", collection, "--identities", identities, "--scenario", "isolation"}
			if failure == "unknown-scenario" {
				args[len(args)-1] = "missing"
			}
			if failure == "deadline" {
				args = append(args, "--timeout", "10ms")
			}
			code := run(args)
			if (code == 0) != (failure == "") {
				t.Fatalf("exit=%d output=%s error=%s", code, stdout.String(), stderr())
			}
			if failure == "unknown-scenario" || failure == "duplicate-subject" || failure == "account-key" || failure == "deadline" {
				if calls != 0 {
					t.Fatal("invalid replay reached management API")
				}
			} else if !strings.Contains(stdout.String(), `"event":"replay_summary"`) {
				t.Fatal("missing replay report")
			}
			if redirected.Load() != 0 {
				t.Fatal("hosted replay followed a redirect")
			}
			for _, secret := range []string{dataAPIReplayJWT("alice"), dataAPIReplayJWT("bob"), "owner-test-token", `"id":1`} {
				if strings.Contains(stdout.String()+stderr(), secret) {
					t.Fatal("replay leaked credentials or response data")
				}
			}
		})
	}
}

func TestDataAPIHostedReplayWorkflowProtectsInputs(t *testing.T) {
	for _, failure := range []string{"both", "overwrite-collection", "overwrite-identities", "missing-jwt", "symlink"} {
		t.Run(failure, func(t *testing.T) {
			path, config := dataAPISyncTestConfig(t)
			root := filepath.Dir(path)
			collection, identities := dataAPIHostedReplayFixture(t, root)
			baseline := filepath.Join(root, "baseline.json")
			if err := os.WriteFile(baseline, dataAPIContractFixture(t, dataAPIDiffFixture()), 0600); err != nil {
				t.Fatal(err)
			}
			original, err := os.ReadFile(collection)
			if err != nil {
				t.Fatal(err)
			}
			config.Coordination = &dataAPISyncCoordination{Pause: config.Migrate, Resume: config.Migrate, Baseline: baseline, AuthorizationReplay: &dataAPIHostedReplayConfig{Collection: collection, Identities: identities, Scenarios: []string{"isolation"}}}
			t.Setenv("GREGALE_DATA_API_REFRESH_TOKEN", strings.Repeat("a", 64))
			switch failure {
			case "both":
				config.Coordination.Authorization = config.Check
			case "overwrite-collection":
				config.Output = collection
			case "overwrite-identities":
				config.Output = identities
			case "missing-jwt":
				t.Setenv("GREGALE_REPLAY_BOB_JWT", "")
			case "symlink":
				alias := filepath.Join(root, "alias.json")
				if err := os.Symlink(collection, alias); err != nil {
					t.Fatal(err)
				}
				config.Coordination.AuthorizationReplay.Collection = alias
			}
			writeDataAPISyncTestConfig(t, path, config)
			if _, err := loadDataAPISyncConfig(path); err == nil {
				t.Fatal("unsafe hosted replay configuration accepted")
			}
			after, err := os.ReadFile(collection)
			if err != nil || string(after) != string(original) {
				t.Fatal("preflight modified saved scenarios")
			}
		})
	}
}

func TestDataAPIHostedReplayMetadata(t *testing.T) {
	for _, name := range []string{"data-api", "account"} {
		command, ok := lookupCliCommand(name)
		if !ok {
			t.Fatalf("missing %s command", name)
		}
		found := false
		for _, sub := range command.Subcommands {
			if sub.Name == "replay" {
				found = true
			}
		}
		if found != (name == "data-api") {
			t.Fatalf("unexpected replay registration in %s", name)
		}
	}
}
