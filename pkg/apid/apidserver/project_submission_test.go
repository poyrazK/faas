package apidserver

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestProjectSubmissionAcceptanceAndDuplicateFence(t *testing.T) {
	s, store, account, key := projectSecretsTestServer(t, api.ScopesAdminOnly)
	attempt := strings.Repeat("a", 32)
	builds := 0
	store.onBuild = func(dep state.Deployment) {
		builds++
		receipt, err := store.ProjectSubmission(t.Context(), account.ID, attempt)
		if err != nil || receipt.Status != "processing" || !receipt.SelectionComplete {
			t.Fatalf("progress=%+v %v", receipt, err)
		}
		found := false
		for _, row := range receipt.Apply.Builds {
			if row.DeploymentID == dep.ID {
				found = true
			}
		}
		if !found {
			t.Fatalf("acceptance not discoverable before progress update: %+v", receipt)
		}
	}
	rec := projectSecretsRequest(t, s, key, "/v1/projects", "", map[string]string{"attempt_id": attempt})
	if rec.Code != http.StatusOK {
		t.Fatalf("apply=%d %s", rec.Code, rec.Body.String())
	}
	var apply api.ApplyResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &apply); err != nil {
		t.Fatal(err)
	}
	if apply.SubmissionAttemptID != attempt || builds != 2 {
		t.Fatalf("apply=%+v builds=%d", apply, builds)
	}
	rec = projectSecretsRequest(t, s, key, "/v1/projects", "", map[string]string{"attempt_id": attempt})
	if rec.Code != http.StatusConflict || builds != 2 {
		t.Fatalf("duplicate=%d builds=%d %s", rec.Code, builds, rec.Body.String())
	}
	request := httptest.NewRequest(http.MethodGet, "/v1/project-submissions/"+attempt, nil)
	request.Header.Set("Authorization", "Bearer "+key)
	rec = httptest.NewRecorder()
	s.handler().ServeHTTP(rec, request)
	if rec.Code != http.StatusOK {
		t.Fatalf("get=%d %s", rec.Code, rec.Body.String())
	}
	var receipt api.ProjectSubmissionReceipt
	if err := json.Unmarshal(rec.Body.Bytes(), &receipt); err != nil {
		t.Fatal(err)
	}
	if receipt.Status != "complete" || len(receipt.Apply.Builds) != 2 {
		t.Fatalf("receipt=%+v", receipt)
	}
	for _, row := range receipt.Apply.Builds {
		if row.DeploymentID == "" || row.BuildID == "" || row.Error != "" {
			t.Fatalf("lost accepted identity: %+v", row)
		}
	}
}

func TestProjectSubmissionLegacyFingerprintCompatible(t *testing.T) {
	s, store, account, key := projectSecretsTestServer(t, api.ScopesAdminOnly)
	attempt := strings.Repeat("d", 32)
	sourceHash := sha256.Sum256(projectSecretArchive(t))
	// The ordinary request fingerprint from ADR-20261019120002, before retry targets.
	payload, err := json.Marshal([]any{hex.EncodeToString(sourceHash[:]), "demo", "", "main", int64(0), map[string]bool{}, map[string]bool{}, false, false, "", (*bool)(nil), map[string]string(nil)})
	if err != nil {
		t.Fatal(err)
	}
	fingerprint := sha256.Sum256(payload)
	if _, err := store.ReserveProjectSubmission(t.Context(), account.ID, attempt, hex.EncodeToString(fingerprint[:]), api.ProjectSubmissionReceipt{AttemptID: attempt, ProjectSlug: "demo", Status: "processing"}); err != nil {
		t.Fatal(err)
	}
	rec := projectSecretsRequest(t, s, key, "/v1/projects", "", map[string]string{"attempt_id": attempt})
	if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "project_submission_exists") {
		t.Fatalf("legacy duplicate=%d %s", rec.Code, rec.Body)
	}
}
