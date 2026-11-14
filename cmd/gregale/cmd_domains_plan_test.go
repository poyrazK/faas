package main_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/api"
)

func TestDomainPlanCLISavedReviewApplyAndTamper(t *testing.T) {
	main.ResetJSONOut(t)
	main.SetPreviewTestAuth(t)
	manifest := api.DomainManifest{Version: 1, Domains: []api.DomainBindingSpec{{Domain: "api.example.com", App: "new-api"}}}
	before := api.DomainBindingTarget{App: "old-api", AppID: "old-id"}
	plan := api.DomainPlan{Version: 1, SHA256: strings.Repeat("a", 64), Manifest: manifest, Changes: []api.DomainBindingChange{{Domain: "api.example.com", Action: "move", Before: &before, After: api.DomainBindingTarget{App: "new-api", AppID: "new-id"}, Verified: true, ClearsDefault: true, DeploymentIDs: []string{"deployment-id"}}}}
	applyCalls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method != "POST" {
			t.Errorf("unexpected method=%s", r.Method)
		}
		switch r.URL.Path {
		case "/v1/domains/plan":
			_ = json.NewEncoder(w).Encode(plan)
		case "/v1/domains/apply":
			applyCalls++
			var req api.DomainApplyRequest
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil || !req.Confirm || req.ExpectedPlanSHA256 != plan.SHA256 || r.Header.Get("Idempotency-Key") != plan.SHA256 {
				t.Errorf("apply request=%+v %v", req, err)
			}
			_ = json.NewEncoder(w).Encode(api.DomainApplyResponse{Plan: plan, Rollback: &api.DomainManifest{Version: 1, Domains: []api.DomainBindingSpec{{Domain: "api.example.com", App: "old-api"}}}})
		default:
			t.Errorf("unexpected path=%s", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	t.Setenv("FAAS_API", server.URL)
	old := *main.OsStdout
	var out bytes.Buffer
	*main.OsStdout = &out
	t.Cleanup(func() { *main.OsStdout = old })
	dir := t.TempDir()
	file := filepath.Join(dir, "domains.yaml")
	saved := filepath.Join(dir, "review.json")
	rollback := filepath.Join(dir, "rollback.json")
	if err := os.WriteFile(file, []byte("version: 1\ndomains:\n  - domain: api.example.com\n    app: new-api\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if code := main.Run([]string{"domain", "plan", "--file", file, "--out", saved}); code != 0 {
		t.Fatalf("plan exit=%d output=%s", code, out.String())
	}
	if !strings.Contains(out.String(), "Clears the source app") {
		t.Fatalf("default URL change not displayed: %s", out.String())
	}
	if info, err := os.Stat(saved); err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("plan permissions=%v %v", info, err)
	}
	blocked := filepath.Join(dir, "existing-rollback.json")
	if err := os.WriteFile(blocked, []byte("keep this file"), 0o600); err != nil {
		t.Fatal(err)
	}
	if code := main.Run([]string{"domain", "apply", "--plan", saved, "--confirm", "--rollback-out", blocked}); code != 1 || applyCalls != 0 {
		t.Fatalf("rollback saving failure changed bindings: exit=%d calls=%d", code, applyCalls)
	}
	if code := main.Run([]string{"domain", "apply", "--plan", saved, "--confirm", "--rollback-out", rollback}); code != 0 || applyCalls != 1 {
		t.Fatalf("apply exit=%d calls=%d", code, applyCalls)
	}
	if body, err := os.ReadFile(rollback); err != nil || !bytes.Contains(body, []byte("old-api")) {
		t.Fatalf("rollback=%s %v", body, err)
	}
	plan.Changes[0].Before = &api.DomainBindingTarget{App: "tampered", AppID: "old-id"}
	if code := main.Run([]string{"domain", "apply", "--plan", saved, "--confirm"}); code != 1 || applyCalls != 1 {
		t.Fatalf("changed diff applied: exit=%d calls=%d", code, applyCalls)
	}
}

func TestDomainMoveDryRunDoesNotApplyOrCreate(t *testing.T) {
	for _, action := range []string{"move", "create"} {
		t.Run(action, func(t *testing.T) {
			main.ResetJSONOut(t)
			main.SetPreviewTestAuth(t)
			p := api.DomainPlan{Version: 1, SHA256: strings.Repeat("b", 64), Manifest: api.DomainManifest{Version: 1, Domains: []api.DomainBindingSpec{{Domain: "api.example.com", App: "target"}}}, Changes: []api.DomainBindingChange{{Domain: "api.example.com", Action: action, After: api.DomainBindingTarget{App: "target", AppID: "target-id"}}}}
			if action == "move" {
				p.Changes[0].Before = &api.DomainBindingTarget{App: "source", AppID: "source-id"}
			}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/v1/domains/plan" {
					t.Errorf("dry-run mutated: %s", r.URL.Path)
				}
				_ = json.NewEncoder(w).Encode(p)
			}))
			defer server.Close()
			t.Setenv("FAAS_API", server.URL)
			old := *main.OsStdout
			var out bytes.Buffer
			*main.OsStdout = &out
			t.Cleanup(func() { *main.OsStdout = old })
			want := 0
			if action == "create" {
				want = 1
			}
			if code := main.Run([]string{"domain", "move", "api.example.com", "--app", "target", "--dry-run"}); code != want {
				t.Fatalf("dry-run=%d want=%d", code, want)
			}
		})
	}
}

func TestDomainPlanRejectsMalformedFileBeforeNetwork(t *testing.T) {
	for _, body := range []string{"version: 1\ndomains: []\n", "version: 1\ndomains: [{domain: api.example.com, app: api, typo: true}]\n", "version: 1\ndomains: [{domain: api.example.com, app: api}]\n---\nversion: 1\n"} {
		main.ResetJSONOut(t)
		file := filepath.Join(t.TempDir(), "invalid.yaml")
		if err := os.WriteFile(file, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		if code := main.Run([]string{"domain", "plan", "--file", file}); code != 1 {
			t.Fatalf("malformed file exit=%d", code)
		}
	}
}
