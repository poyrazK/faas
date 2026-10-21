package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

const policyJournalManifest = "lifecycle:\n  profiling:\n    enabled: true\n    window_seconds: 10\n  tracing:\n    enabled: true\n    sample_ratio: 0.25\n"

type policyJournalFixture struct {
	store        *state.MemStore
	app          state.App
	mu           sync.Mutex
	submissions  int
	stageEntered chan struct{}
	releaseStage chan struct{}
	lateStage    chan error
	holdStage    bool
	late         bool
	loseCleanup  bool
}

func newPolicyJournalFixture(t *testing.T) *policyJournalFixture {
	t.Helper()
	s := state.NewMemStore()
	a, err := s.CreateAccount(t.Context(), "journal@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := s.CreateApp(t.Context(), state.App{AccountID: a.ID, Slug: "demo"})
	if err != nil {
		t.Fatal(err)
	}
	return &policyJournalFixture{store: s, app: app, stageEntered: make(chan struct{}, 1), releaseStage: make(chan struct{}), lateStage: make(chan error, 1)}
}

func policyJournalPrepare(app state.App, fields map[string]json.RawMessage) (state.AppManifest, error) {
	m := app.Manifest
	for field, value := range fields {
		if field == "profiling" {
			if err := json.Unmarshal(value, &m.Profiling); err != nil {
				return m, err
			}
		}
		if field == "tracing" {
			if err := json.Unmarshal(value, &m.Tracing); err != nil {
				return m, err
			}
		}
	}
	return m, nil
}

func (f *policyJournalFixture) serve(w http.ResponseWriter, r *http.Request) {
	encode := func(v any) { _ = json.NewEncoder(w).Encode(v) }
	if r.URL.Path == "/v1/apps/demo" && r.Method == "GET" {
		app, err := f.store.AppByID(r.Context(), f.app.ID)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		encode(api.AppResponse{ID: app.ID, Slug: app.Slug, Manifest: api.AppManifest{Profiling: app.Manifest.Profiling, Tracing: app.Manifest.Tracing, RequestTimeoutS: app.Manifest.RequestTimeoutS}})
		return
	}
	if r.URL.Path == "/v1/apps/demo/deployment-staging" {
		if r.Method == "GET" {
			snapshot, err := f.store.DeploymentStagingSnapshot(r.Context(), f.app.AccountID, f.app.ID)
			if err != nil {
				http.Error(w, err.Error(), 503)
				return
			}
			encode(snapshot)
			return
		}
		var req api.DeploymentStagingRequest
		_ = json.NewDecoder(r.Body).Decode(&req)
		if req.Operation == "stage" && f.late {
			f.stageEntered <- struct{}{}
			<-f.releaseStage
		}
		result, err := f.store.ApplyDeploymentStaging(context.WithoutCancel(r.Context()), f.app.ID, state.DeploymentStagingParams{AccountID: f.app.AccountID, Request: req, Prepare: policyJournalPrepare})
		if req.Operation == "stage" && f.late {
			f.lateStage <- err
		}
		if err != nil {
			api.WriteProblem(w, api.NewProblem(409, api.CodeDeploymentStagingConflict, "Policy conflict", err.Error()))
			return
		}
		if req.Operation == "stage" && f.holdStage {
			f.stageEntered <- struct{}{}
			<-r.Context().Done()
			return
		}
		if req.Operation == "restore" && f.loseCleanup {
			f.loseCleanup = false
			api.WriteProblem(w, api.ErrCapacity("cleanup reply lost after applying"))
			return
		}
		encode(result.Receipt)
		return
	}
	if r.Method == "POST" && strings.HasPrefix(r.URL.Path, "/v1/apps/demo/deployments") {
		f.mu.Lock()
		f.submissions++
		f.mu.Unlock()
		encode(api.DeploymentResponse{ID: "dep-1", AppID: f.app.ID, Status: "live"})
		return
	}
	http.NotFound(w, r)
}

func TestDeployPolicyJournalAcrossProcesses(t *testing.T) {
	for _, mode := range []string{"lost-stage-reply", "late-stage", "user-edit"} {
		t.Run(mode, func(t *testing.T) {
			f := newPolicyJournalFixture(t)
			f.holdStage = mode != "late-stage"
			f.late = mode == "late-stage"
			srv := httptest.NewServer(http.HandlerFunc(f.serve))
			defer srv.Close()
			stateDir, sourceDir := t.TempDir(), t.TempDir()
			for name, content := range map[string]string{"package.json": "{}", "index.js": "console.log('fixture')", "gregale.yaml": policyJournalManifest} {
				if err := os.WriteFile(filepath.Join(sourceDir, name), []byte(content), 0600); err != nil {
					t.Fatal(err)
				}
			}
			binary, err := os.Executable()
			if err != nil {
				t.Fatal(err)
			}
			command := func(action, dir string) *exec.Cmd {
				cmd := exec.CommandContext(t.Context(), binary, "-test.run=^TestDeployPolicyJournalProcessHelper$")
				cmd.Dir = dir
				cmd.Env = append(os.Environ(), "GREGALE_POLICY_JOURNAL_MODE="+action, "GREGALE_POLICY_JOURNAL_CONFLICT="+mode, "GREGALE_POLICY_JOURNAL_STATE="+stateDir, "FAAS_API="+srv.URL, "FAAS_TOKEN=fp_journal_fixture")
				return cmd
			}
			child := command("submit", sourceDir)
			var output bytes.Buffer
			child.Stdout, child.Stderr = &output, &output
			if err := child.Start(); err != nil {
				t.Fatal(err)
			}
			select {
			case <-f.stageEntered:
			case <-time.After(20 * time.Second):
				_ = child.Process.Kill()
				_ = child.Wait()
				t.Fatalf("stage did not reach server: %s", &output)
			}
			if err := child.Process.Kill(); err != nil {
				t.Fatal(err)
			}
			_ = child.Wait()
			files, err := filepath.Glob(filepath.Join(stateDir, "gregale", "deploys", "*", "*.json"))
			if err != nil || len(files) != 1 {
				t.Fatalf("sessions=%v err=%v", files, err)
			}
			data, err := os.ReadFile(files[0])
			if err != nil {
				t.Fatal(err)
			}
			var record deploySessionRecord
			if err := json.Unmarshal(data, &record); err != nil {
				t.Fatal(err)
			}
			if record.PolicyJournal == nil || record.PolicyJournal.Phase != deployPoliciesPrepared || record.DeploymentID != "" {
				t.Fatalf("missing pre-mutation journal: %+v", record)
			}
			if bytes.Contains(data, []byte(sourceDir)) || bytes.Contains(data, []byte("fp_journal_fixture")) {
				t.Fatalf("private journal contains source/credential: %s", data)
			}
			info, err := os.Stat(files[0])
			if err != nil || info.Mode().Perm() != 0600 {
				t.Fatalf("journal permissions: %v err=%v", info, err)
			}
			if mode == "user-edit" {
				app, _ := f.store.AppByID(t.Context(), f.app.ID)
				m := app.Manifest
				m.Profiling = &api.ProfilingConfig{Enabled: true, WindowSeconds: 20}
				if _, err := f.store.UpdateApp(t.Context(), f.app.ID, state.UpdateAppParams{Manifest: &m}); err != nil {
					t.Fatal(err)
				}
			}
			if err := os.RemoveAll(sourceDir); err != nil {
				t.Fatal(err)
			}
			if data, err := command("resume", t.TempDir()).CombinedOutput(); err != nil {
				t.Fatalf("resume=%v output=%s", err, data)
			}
			if mode == "late-stage" {
				close(f.releaseStage)
				select {
				case err := <-f.lateStage:
					if !errors.Is(err, state.ErrDeploymentStagingConflict) {
						t.Fatalf("late stage=%v", err)
					}
				case <-time.After(5 * time.Second):
					t.Fatal("delayed stage did not finish")
				}
			}
			app, _ := f.store.AppByID(t.Context(), f.app.ID)
			if mode == "user-edit" {
				if app.Manifest.Profiling == nil || app.Manifest.Profiling.WindowSeconds != 20 {
					t.Fatal("cleanup overwrote user edit")
				}
			} else if app.Manifest.Profiling != nil || app.Manifest.Tracing != nil {
				t.Fatalf("cleanup left policies: %+v", app.Manifest)
			}
			f.mu.Lock()
			defer f.mu.Unlock()
			if f.submissions != 0 {
				t.Fatalf("cleanup created %d deployments", f.submissions)
			}
		})
	}
}

func TestDeployPolicyJournalProcessHelper(t *testing.T) {
	mode := os.Getenv("GREGALE_POLICY_JOURNAL_MODE")
	if mode == "" {
		t.Skip("process helper")
	}
	t.Setenv("XDG_STATE_HOME", os.Getenv("GREGALE_POLICY_JOURNAL_STATE"))
	jsonOutput = true
	var output bytes.Buffer
	osStdout, osStderr = &output, &output
	if mode == "submit" {
		code := cmdDeployTarballToExisting(t.Context(), []string{"--name", "demo", "--path", ".", "--no-doctor", "--secret-scan", "off"}, true, deployExecution{})
		t.Fatalf("submit returned before crash: code=%d output=%s", code, &output)
	}
	code := cmdDeployResume(t.Context(), []string{"--app", "demo"})
	if os.Getenv("GREGALE_POLICY_JOURNAL_CONFLICT") == "user-edit" {
		if code == 0 {
			t.Fatal("conflicting cleanup succeeded")
		}
		return
	}
	if code != 0 {
		t.Fatalf("cleanup exit=%d output=%s", code, &output)
	}
	var receipt map[string]any
	if err := json.Unmarshal(output.Bytes(), &receipt); err != nil {
		t.Fatal(err)
	}
	if receipt["policy_cleanup_complete"] != true || receipt["deployment_created"] != false || receipt["submission_outcome"] != "not_submitted" {
		t.Fatalf("receipt=%v", receipt)
	}
}

func TestDeployPolicyJournalLostCleanupReply(t *testing.T) {
	f := newPolicyJournalFixture(t)
	srv := httptest.NewServer(http.HandlerFunc(f.serve))
	defer srv.Close()
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_journal_fixture")
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	c, err := authedClient()
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "gregale.yaml"), []byte(policyJournalManifest), 0600); err != nil {
		t.Fatal(err)
	}
	s, lock, err := newSavedDeploySession(t.Context(), c, "demo", f.app.ID, dir, "intent", nil, false, false)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = lock.Close() }()
	if err := s.preparePolicyJournal(t.Context(), c, dir); err != nil {
		t.Fatal(err)
	}
	if err := s.stagePolicies(t.Context(), c); err != nil {
		t.Fatal(err)
	}
	f.loseCleanup = true
	if err := s.cleanPolicies(t.Context(), c); err == nil {
		t.Fatal("lost cleanup reply concealed")
	}
	pending, err := loadSavedDeploySession(c, s.path)
	if err != nil {
		t.Fatal(err)
	}
	if pending.record.PolicyJournal.Cleaned {
		t.Fatal("lost reply marked completed locally")
	}
	if err := pending.cleanPolicies(t.Context(), c); err != nil {
		t.Fatal(err)
	}
	app, _ := f.store.AppByID(t.Context(), f.app.ID)
	if app.Manifest.Profiling != nil || app.Manifest.Tracing != nil {
		t.Fatal("cleanup not restored")
	}
}
