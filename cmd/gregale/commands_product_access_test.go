// ADR-1542: reviewed customer-permission changes preserve app and catalog identity.
package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/productcontrol"
)

func TestProductAccessCLIReviewAndApply(t *testing.T) {
	base, err := productcontrol.Compile(api.ProductCatalogSpec{AuthApp: "auth", Currency: "EUR", Features: map[string]api.ProductFeature{"export": {Label: "Export", Operation: &api.ProductOperationBinding{Name: "export", Scope: api.DefaultEnvScope}}}, Plans: map[string]api.ProductPlan{"starter": {Label: "Starter", Features: map[string]api.ProductFeatureGrant{"export": {MonthlyStarts: 10, UnitPriceMillicents: 25000}}}}})
	if err != nil {
		t.Fatal(err)
	}
	var applies, previews int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer owner" {
			t.Error("owner credential missing")
		}
		switch {
		case r.Method == "GET" && r.URL.Path == "/v1/apps/exports/product":
			if r.URL.Query().Get("revision") != base.Revision {
				t.Error("confirmation did not read its immutable base")
			}
			_ = json.NewEncoder(w).Encode(base)
		case r.Method == "POST" && r.URL.Path == "/v1/apps/exports/product/access/preview":
			previews++
			var policy api.ProductAccessPolicy
			_ = json.NewDecoder(r.Body).Decode(&policy)
			preview, err := productcontrol.PreviewAccess(base, policy)
			if err != nil {
				t.Error(err)
				w.WriteHeader(400)
				return
			}
			_ = json.NewEncoder(w).Encode(preview)
		case r.Method == "PUT" && r.URL.Path == "/v1/apps/exports/product/access":
			applies++
			var change api.ProductAccessChangeRequest
			_ = json.NewDecoder(r.Body).Decode(&change)
			preview, err := productcontrol.PreviewAccess(base, change.Policy)
			if err != nil || preview.Change.ExpectedHead != change.ExpectedHead || preview.Change.ExpectedRevision != change.ExpectedRevision {
				t.Error("confirmation changed reviewed policy or revision")
			}
			_ = json.NewEncoder(w).Encode(preview.After)
		default:
			t.Errorf("unexpected product access route %s %s", r.Method, r.URL)
			w.WriteHeader(404)
		}
	}))
	defer srv.Close()
	client := NewClient(srv.URL, "owner")
	dir := t.TempDir()
	policyPath, reviewPath := filepath.Join(dir, "access.json"), filepath.Join(dir, "review.json")
	if err := os.WriteFile(policyPath, []byte(`{"roles":{"owner":["work:run","billing:read"],"operator":["work:run"],"billing":["billing:read"]}}`), 0600); err != nil {
		t.Fatal(err)
	}
	c, err := parseProductCommand([]string{"access", "review", "--app", "exports", "--policy-file", policyPath, "--file", reviewPath})
	if err != nil {
		t.Fatal(err)
	}
	value, err := runProductCommand(t.Context(), client, c)
	if err != nil {
		t.Fatal(err)
	}
	if previews != 1 || applies != 0 {
		t.Fatal("review performed an access mutation")
	}
	info, err := os.Stat(reviewPath)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatal("review file is not private", err)
	}
	var human bytes.Buffer
	if err := writeProductOutput(&human, value, false); err != nil {
		t.Fatal(err)
	}
	for _, text := range []string{"Current permissions:", "Proposed permissions:", "operator", "View work, Run work", "View billing"} {
		if !strings.Contains(human.String(), text) {
			t.Fatalf("review output missing %q: %s", text, human.String())
		}
	}
	original, err := os.ReadFile(reviewPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = runProductCommand(t.Context(), client, c); err == nil {
		t.Fatal("review overwrote an existing file")
	}
	unchanged, err := os.ReadFile(reviewPath)
	if err != nil || !bytes.Equal(original, unchanged) {
		t.Fatal("existing review changed", err)
	}
	apply, err := parseProductCommand([]string{"access", "apply", "--app", "exports", "--file", reviewPath})
	if err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if _, err = runProductCommand(t.Context(), client, apply); err != nil {
			t.Fatal(err)
		}
	}
	if applies != 2 {
		t.Fatal("same review could not be retried")
	}
	wrongApp := apply
	wrongApp.app = "other"
	if _, err = runProductCommand(t.Context(), client, wrongApp); err == nil || applies != 2 {
		t.Fatal("review was applied to another product")
	}
	var saved productAccessReviewFile
	if err = json.Unmarshal(original, &saved); err != nil {
		t.Fatal(err)
	}
	saved.Preview.After.Policy.Roles["operator"] = []api.ProductPermission{api.ProductPermissionViewBilling}
	tampered, err := json.Marshal(saved)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(reviewPath, tampered, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = runProductCommand(t.Context(), client, apply); err == nil || applies != 2 {
		t.Fatal("inconsistent reviewed permission was submitted")
	}
	if err = os.WriteFile(reviewPath, []byte(`{"app":"exports","app":"other"}`), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = runProductCommand(t.Context(), client, apply); err == nil || applies != 2 {
		t.Fatal("duplicate JSON selectors were submitted")
	}
}

func TestProductAccessCLIArguments(t *testing.T) {
	for _, args := range [][]string{
		{"access"},
		{"access", "inspect"},
		{"access", "review", "--app", "exports"},
		{"access", "apply", "--app", "exports", "--policy-file", "new.json"},
		{"access", "apply", "--app", "exports", "--file", ""},
	} {
		if _, err := parseProductCommand(args); err == nil {
			t.Fatalf("accepted %v", args)
		}
	}
}
