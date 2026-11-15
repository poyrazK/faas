package apidserver_test

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
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/reposcan"
)

func TestSDKProjectExampleScansAPIWorkerAndPostgres(t *testing.T) {
	root := filepath.Join("..", "..", "..", "examples", "sdk-project-application", "source")
	result, err := reposcan.Scan(os.DirFS(root))
	if err != nil {
		t.Fatal(err)
	}
	classes := map[string]reposcan.Class{}
	for _, workload := range result.Workloads {
		classes[workload.Name] = workload.Class
	}
	if len(classes) != 2 || classes["api"] != reposcan.ClassHTTP || classes["worker"] != reposcan.ClassWorker {
		t.Fatalf("example workloads: %+v", result.Workloads)
	}
	if len(result.Managed) != 1 || result.Managed[0].Name != "db" || string(result.Managed[0].Kind) != "postgres" {
		t.Fatalf("example managed services: %+v", result.Managed)
	}
}

func TestCachedProjectApplyTracksDurableAcceptance(t *testing.T) {
	t.Cleanup(apidserver.WithTestRecipient(t))
	t.Setenv("FAAS_SCAN_SPOOL_ROOT", t.TempDir())
	t.Setenv("FAAS_SPOOL_ROOT", t.TempDir())
	e, _ := apidserver.NewTestServerWithCapturingNotifier(t, api.PlanPro)
	scan := migrationUpload(t, *e.H(), *e.Key(), "/v1/projects/scan", map[string]string{"project_slug": "sdk-project", "no_triggers": "true"})
	if scan.Code != http.StatusOK {
		t.Fatalf("scan=%d %s", scan.Code, scan.Body)
	}
	var plan api.PlanResponse
	if err := json.Unmarshal(scan.Body.Bytes(), &plan); err != nil {
		t.Fatal(err)
	}
	attempt := strings.Repeat("c", 32)
	post := func(identity string) *httptest.ResponseRecorder {
		body, err := json.Marshal(api.ApplyCachedProjectPlanRequest{PlanToken: plan.PlanToken})
		if err != nil {
			t.Fatal(err)
		}
		req := httptest.NewRequest(http.MethodPost, "/v1/projects/apply-plan", bytes.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+*e.Key())
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Deployment-Attempt-ID", attempt)
		req.Header.Set("Idempotency-Key", identity)
		rec := httptest.NewRecorder()
		(*e.H()).ServeHTTP(rec, req)
		return rec
	}
	accepted := post("cached-first")
	if accepted.Code != http.StatusOK {
		t.Fatalf("apply=%d %s", accepted.Code, accepted.Body)
	}
	var applied api.ApplyResponse
	if err := json.Unmarshal(accepted.Body.Bytes(), &applied); err != nil {
		t.Fatal(err)
	}
	if applied.SubmissionAttemptID != attempt || len(applied.Builds) != 1 || applied.Builds[0].DeploymentID == "" {
		t.Fatalf("missing durable IDs: %+v", applied)
	}
	req := httptest.NewRequest(http.MethodGet, "/v1/project-submissions/"+attempt, nil)
	req.Header.Set("Authorization", "Bearer "+*e.Key())
	rec := httptest.NewRecorder()
	(*e.H()).ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("receipt=%d %s", rec.Code, rec.Body)
	}
	var receipt api.ProjectSubmissionReceipt
	if err := json.Unmarshal(rec.Body.Bytes(), &receipt); err != nil {
		t.Fatal(err)
	}
	if receipt.Status != "complete" || !receipt.SelectionComplete || len(receipt.Apply.Builds) != 1 || receipt.Apply.Builds[0].DeploymentID != applied.Builds[0].DeploymentID {
		t.Fatalf("lost accepted deployment: %+v", receipt)
	}
	// A different HTTP key cannot bypass the durable attempt fence.
	duplicate := post("cached-second")
	if duplicate.Code != http.StatusConflict || !strings.Contains(duplicate.Body.String(), "project_submission_exists") {
		t.Fatalf("duplicate=%d %s", duplicate.Code, duplicate.Body)
	}
	store := *e.Store()
	other, err := store.CreateAccount(t.Context(), "sdk-other@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	otherKey, hash, err := api.GenerateAPIKey()
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.CreateAPIKey(t.Context(), other.ID, hash, "other", api.ScopesAdminOnly); err != nil {
		t.Fatal(err)
	}
	req = httptest.NewRequest(http.MethodGet, "/v1/project-submissions/"+attempt, nil)
	req.Header.Set("Authorization", "Bearer "+otherKey)
	rec = httptest.NewRecorder()
	(*e.H()).ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("cross-account receipt=%d %s", rec.Code, rec.Body)
	}
}
