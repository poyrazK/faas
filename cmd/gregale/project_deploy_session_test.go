package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestProjectDeploySessionResumeReadsOnlyAndRetainsPartialFailure(t *testing.T) {
	t.Chdir(t.TempDir())
	var reads int
	activated := api.NewDeploymentConfigurationStatus("activated", "")
	deployReliabilityCLI(t, func(w http.ResponseWriter, r *http.Request) {
		reads++
		if r.Method != http.MethodGet || r.URL.Path != "/v1/deployments/accepted" {
			t.Errorf("resume made unexpected request %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(api.DeploymentResponse{ID: "accepted", AppID: "app", Status: statusLive, Configuration: &activated})
	})
	c, err := authedClient()
	if err != nil {
		t.Fatal(err)
	}
	pending := api.NewDeploymentConfigurationStatus("prepared", "awaiting_activation")
	apply := api.ApplyResponse{ProjectID: "project", Builds: []api.AppliedBuild{
		{Slug: "healthy", AppID: "app", DeploymentID: "accepted", Configuration: &pending},
		{Slug: "failed", AppID: "other", Error: "secret-sentinel-build-output"},
	}}
	s, lock, err := newSavedProjectDeploy(c, "demo", "production", apply)
	if err != nil {
		t.Fatal(err)
	}
	_ = lock.Close()
	data, err := os.ReadFile(s.path)
	if err != nil || bytes.Contains(data, []byte("secret-sentinel")) || bytes.Contains(data, []byte("fp_live_fixture")) {
		t.Fatalf("unsafe journal=%s err=%v", data, err)
	}
	info, err := os.Stat(s.path)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("session permissions=%v err=%v", info, err)
	}
	// Load a fresh session through the command; no source or original apply is used.
	for i := 0; i < 2; i++ {
		osStdout.(*bytes.Buffer).Reset()
		if code := cmdDeployResume(t.Context(), []string{s.record.ID, "--timeout", "1"}); code != 1 {
			t.Fatalf("partial resume exit=%d", code)
		}
		var receipt projectDeployReceipt
		if err := json.Unmarshal(osStdout.(*bytes.Buffer).Bytes(), &receipt); err != nil {
			t.Fatal(err)
		}
		if receipt.SessionID != s.record.ID || !receipt.RecoverySaved || receipt.Builds[0].Configuration.Crons.Status != "activated" || receipt.Builds[1].Error == "" {
			t.Fatalf("receipt=%+v", receipt)
		}
	}
	if reads != 2 {
		t.Fatalf("unexpected reads=%d", reads)
	}
}

func TestProjectDeploySessionLockAndConnectionIsolation(t *testing.T) {
	deployReliabilityCLI(t, func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("unexpected request %s", r.URL.Path)
		http.NotFound(w, r)
	})
	c, err := authedClient()
	if err != nil {
		t.Fatal(err)
	}
	s, lock, err := newSavedProjectDeploy(c, "demo", "", api.ApplyResponse{})
	if err != nil {
		t.Fatal(err)
	}
	if code := cmdDeployResume(t.Context(), []string{s.record.ID}); code == 0 {
		t.Fatal("resume ignored session lock")
	}
	_ = lock.Close()
	other := NewClient(c.BaseURL(), "different-credential")
	if _, err := loadSavedDeploySession(other, s.path); err == nil {
		t.Fatal("accepted a different connection")
	}
	if _, err := selectDeploySession(c, nil, "demo"); err == nil {
		t.Fatal("--app selected a project session")
	}
	s.record.Project.Workloads = []savedProjectWorkload{{Slug: "demo", DeploymentID: "../../unsafe"}}
	if err := s.save(); err != nil {
		t.Fatal(err)
	}
	if _, err := loadSavedDeploySession(c, s.path); err == nil {
		t.Fatal("accepted unsafe deployment identity")
	}
}

func TestProjectDeploySessionSavedBeforeWaitAndNoWaitRecovery(t *testing.T) {
	t.Chdir(t.TempDir())
	var sessionID string
	var reads, submits int
	pending := api.NewDeploymentConfigurationStatus("prepared", "awaiting_activation")
	activated := api.NewDeploymentConfigurationStatus("activated", "")
	deployReliabilityCLI(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/projects/scan":
			_ = json.NewEncoder(w).Encode(api.PlanResponse{CanApply: true, PlanToken: "plan", Workloads: []api.PlanWorkload{{Name: "demo"}}})
		case "/v1/projects":
			submits++
			_ = json.NewEncoder(w).Encode(api.ApplyResponse{ProjectID: "project", Builds: []api.AppliedBuild{{Slug: "demo", AppID: "app", DeploymentID: "dep", Configuration: &pending}}})
		case "/v1/deployments/dep":
			reads++
			c, _ := authedClient()
			dir, _ := deploySessionDir(c)
			entries, _ := os.ReadDir(dir)
			for _, entry := range entries {
				if strings.HasSuffix(entry.Name(), ".json") {
					sessionID = strings.TrimSuffix(entry.Name(), ".json")
				}
			}
			if sessionID == "" {
				t.Error("wait started before receipt was saved")
			}
			_ = json.NewEncoder(w).Encode(api.DeploymentResponse{ID: "dep", AppID: "app", Status: statusLive, Configuration: &activated})
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
		}
	})
	archive := writeDeploySourceArchive(t, map[string]string{"package.json": "{}"})
	if code := cmdDeployTarball([]string{"--name", "demo", "--project", "--tarball", archive, "--yes", "--no-wait", "--json"}); code != 0 {
		t.Fatalf("deploy exit=%d", code)
	}
	var receipt projectDeployReceipt
	if err := json.Unmarshal(osStdout.(*bytes.Buffer).Bytes(), &receipt); err != nil {
		t.Fatal(err)
	}
	if reads != 0 || !receipt.RecoverySaved || receipt.SessionID == "" || receipt.ResumeCommand == "" {
		t.Fatalf("receipt=%+v reads=%d", receipt, reads)
	}
	if err := os.Remove(archive); err != nil {
		t.Fatal(err)
	}
	osStdout.(*bytes.Buffer).Reset()
	if code := cmdDeployResume(t.Context(), []string{receipt.SessionID, "--timeout", "1"}); code != 0 {
		t.Fatalf("resume exit=%d", code)
	}
	if submits != 1 || reads != 1 || sessionID != receipt.SessionID {
		t.Fatalf("submits=%d reads=%d session=%s", submits, reads, sessionID)
	}
}

func TestProjectDeployReceiptSaveFailurePreservesAcceptedIDs(t *testing.T) {
	deployReliabilityCLI(t, func(w http.ResponseWriter, r *http.Request) { t.Error("save failure made a request") })
	code := renderProjectDeployReceipt(api.PlanResponse{}, api.ApplyResponse{Builds: []api.AppliedBuild{{Slug: "demo", DeploymentID: "accepted"}}}, nil, false, os.ErrPermission)
	var receipt projectDeployReceipt
	if err := json.Unmarshal(osStdout.(*bytes.Buffer).Bytes(), &receipt); err != nil {
		t.Fatal(err)
	}
	if code == 0 || receipt.RecoverySaved || receipt.Builds[0].DeploymentID != "accepted" {
		t.Fatalf("exit=%d receipt=%+v", code, receipt)
	}
}

func TestProjectDeployResumeTimeoutKeepsAcceptedSession(t *testing.T) {
	pending := api.NewDeploymentConfigurationStatus("prepared", "awaiting_activation")
	deployReliabilityCLI(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Error("resume mutated server")
		}
		_ = json.NewEncoder(w).Encode(api.DeploymentResponse{ID: "dep", AppID: "app", Status: statusLive, Configuration: &pending})
	})
	c, err := authedClient()
	if err != nil {
		t.Fatal(err)
	}
	s, lock, err := newSavedProjectDeploy(c, "demo", "", api.ApplyResponse{Builds: []api.AppliedBuild{{Slug: "demo", AppID: "app", DeploymentID: "dep", Configuration: &pending}}})
	if err != nil {
		t.Fatal(err)
	}
	_ = lock.Close()
	if code := resumeSavedProjectDeploy(t.Context(), c, s, 20*time.Millisecond); code != 3 {
		t.Fatalf("timeout exit=%d", code)
	}
	fresh, err := loadSavedDeploySession(c, s.path)
	if err != nil || fresh.record.Project.Workloads[0].DeploymentID != "dep" {
		t.Fatalf("lost accepted session: %v", err)
	}
}

func TestProjectDeploySessionSavedBeforeInitialWait(t *testing.T) {
	t.Chdir(t.TempDir())
	var saved bool
	deployReliabilityCLI(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/projects/scan":
			_ = json.NewEncoder(w).Encode(api.PlanResponse{CanApply: true, PlanToken: "plan", Workloads: []api.PlanWorkload{{Name: "demo"}}})
		case "/v1/projects":
			_ = json.NewEncoder(w).Encode(api.ApplyResponse{Builds: []api.AppliedBuild{{Slug: "demo", AppID: "app", DeploymentID: "dep"}}})
		case "/v1/deployments/dep":
			c, _ := authedClient()
			dir, _ := deploySessionDir(c)
			entries, _ := os.ReadDir(dir)
			for _, entry := range entries {
				if strings.HasSuffix(entry.Name(), ".json") {
					saved = true
				}
			}
			_ = json.NewEncoder(w).Encode(api.DeploymentResponse{ID: "dep", AppID: "app", Status: statusLive})
		default:
			t.Errorf("unexpected request %s", r.URL.Path)
			http.NotFound(w, r)
		}
	})
	archive := writeDeploySourceArchive(t, map[string]string{"package.json": "{}"})
	if code := cmdDeployTarball([]string{"--name", "demo", "--project", "--tarball", archive, "--yes", "--json"}); code != 0 || !saved {
		t.Fatalf("exit=%d journal-before-wait=%v", code, saved)
	}
}

func TestProjectDeploySessionUsesProjectSlugContract(t *testing.T) {
	deployReliabilityCLI(t, func(w http.ResponseWriter, r *http.Request) { t.Error("session load made a request") })
	c, err := authedClient()
	if err != nil {
		t.Fatal(err)
	}
	for _, slug := range []string{"x", strings.Repeat("p", 63), "tag-project"} {
		s, lock, err := newSavedProjectDeploy(c, slug, "", api.ApplyResponse{})
		if err != nil {
			t.Fatalf("save %s: %v", slug, err)
		}
		_ = lock.Close()
		if _, err := loadSavedDeploySession(c, s.path); err != nil {
			t.Fatalf("load %s: %v", slug, err)
		}
	}
}

func TestProjectDeployLostApplyReplyRecoveredByReceipt(t *testing.T) {
	t.Chdir(t.TempDir())
	var attempt string
	var applies, receiptReads int
	activated := api.NewDeploymentConfigurationStatus("activated", "")
	deployReliabilityCLI(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/v1/projects/scan":
			_ = json.NewEncoder(w).Encode(api.PlanResponse{CanApply: true, PlanToken: "plan", Workloads: []api.PlanWorkload{{Name: "demo"}}})
		case r.URL.Path == "/v1/projects":
			applies++
			attempt = r.Header.Get(api.DeploymentAttemptHeader)
			c, _ := authedClient()
			dir, _ := deploySessionDir(c)
			entries, _ := os.ReadDir(dir)
			if len(entries) == 0 {
				t.Error("submission began before attempt was saved")
			}
			// Simulate accepted server work with an unusable response body.
			_, _ = w.Write([]byte("lost reply"))
		case strings.HasPrefix(r.URL.Path, "/v1/project-submissions/"):
			receiptReads++
			if r.Method != http.MethodGet || !strings.HasSuffix(r.URL.Path, attempt) {
				t.Error("wrong receipt request")
			}
			_ = json.NewEncoder(w).Encode(api.ProjectSubmissionReceipt{AttemptID: attempt, ProjectSlug: "demo", Status: "complete", Apply: api.ApplyResponse{ProjectID: "project", SubmissionAttemptID: attempt, Builds: []api.AppliedBuild{{Slug: "demo", AppID: "app", DeploymentID: "dep"}}}})
		case r.URL.Path == "/v1/deployments/dep":
			_ = json.NewEncoder(w).Encode(api.DeploymentResponse{ID: "dep", AppID: "app", Status: statusLive, Configuration: &activated})
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
		}
	})
	archive := writeDeploySourceArchive(t, map[string]string{"package.json": "{}"})
	if code := cmdDeployTarball([]string{"--name", "demo", "--project", "--tarball", archive, "--yes", "--json"}); code == 0 {
		t.Fatal("lost reply claimed success")
	}
	var initial projectDeployReceipt
	if err := json.Unmarshal(osStdout.(*bytes.Buffer).Bytes(), &initial); err != nil {
		t.Fatal(err)
	}
	if initial.SessionID == "" || !initial.RecoverySaved || !api.ValidDeploymentAttemptID(attempt) {
		t.Fatalf("initial=%+v attempt=%q", initial, attempt)
	}
	_ = os.Remove(archive)
	osStdout.(*bytes.Buffer).Reset()
	if code := cmdDeployResume(t.Context(), []string{initial.SessionID, "--timeout", "2"}); code != 0 {
		t.Fatalf("resume exit=%d", code)
	}
	var recovered projectDeployReceipt
	if err := json.Unmarshal(osStdout.(*bytes.Buffer).Bytes(), &recovered); err != nil {
		t.Fatal(err)
	}
	if applies != 1 || receiptReads != 1 || recovered.Builds[0].DeploymentID != "dep" {
		t.Fatalf("applies=%d receiptReads=%d recovered=%+v", applies, receiptReads, recovered)
	}
}
