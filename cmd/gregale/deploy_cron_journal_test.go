package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

const cronJournalManifest = "triggers:\n  - kind: cron\n    app: demo\n    schedule: '0 * * * *'\n    path: /new\n"

func TestDeployCronJournalCrashRecovery(t *testing.T) {
	for _, mode := range []string{"lost-stage-reply", "late-stage", "user-edit", "lost-cleanup-reply", "user-edit-with-policies"} {
		t.Run(mode, func(t *testing.T) {
			f := newPolicyJournalFixture(t)
			ctx := t.Context()
			for _, path := range []string{"/a", "/b"} {
				if _, err := f.store.CreateCron(ctx, f.app.ID, "* * * * *", path, true); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := f.store.CreateCronWithOptions(ctx, f.app.ID, "* * * * *", "", true, state.CronOptions{Command: []string{"echo", "secret-command-sentinel"}}); err != nil {
				t.Fatal(err)
			}
			before, err := f.store.DeploymentCronStagingSnapshot(ctx, f.app.AccountID, f.app.ID)
			if err != nil {
				t.Fatal(err)
			}
			entered, release, lateResult := make(chan struct{}, 1), make(chan struct{}), make(chan error, 1)
			loseCleanup := mode == "lost-cleanup-reply"
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if !strings.HasSuffix(r.URL.Path, "/deployment-cron-staging") {
					f.serve(w, r)
					return
				}
				if r.Method == "GET" {
					snapshot, err := f.store.DeploymentCronStagingSnapshot(r.Context(), f.app.AccountID, f.app.ID)
					if err != nil {
						http.Error(w, err.Error(), 503)
						return
					}
					_ = json.NewEncoder(w).Encode(snapshot)
					return
				}
				var request api.DeploymentCronStagingRequest
				_ = json.NewDecoder(r.Body).Decode(&request)
				if request.Operation == "prepare" && mode == "late-stage" {
					entered <- struct{}{}
					<-release
				}
				result, err := f.store.ApplyDeploymentCronStaging(context.WithoutCancel(r.Context()), f.app.ID, state.DeploymentCronStagingParams{AccountID: f.app.AccountID, Request: request, Limits: api.MustLimitsFor(api.PlanPro)})
				if request.Operation == "prepare" && mode == "late-stage" {
					lateResult <- err
				}
				if err != nil {
					api.WriteProblem(w, api.NewProblem(409, api.CodeDeploymentCronConflict, "Cron conflict", err.Error()))
					return
				}
				if request.Operation == "prepare" && mode != "late-stage" {
					entered <- struct{}{}
					<-r.Context().Done()
					return
				}
				if request.Operation == "cancel" && loseCleanup {
					loseCleanup = false
					api.WriteProblem(w, api.ErrCapacity("cleanup reply lost after apply"))
					return
				}
				_ = json.NewEncoder(w).Encode(result.Receipt)
			}))
			defer srv.Close()
			manifest := cronJournalManifest
			if mode == "user-edit-with-policies" {
				manifest += policyJournalManifest
			}
			source := t.TempDir()
			stateDir := t.TempDir()
			for name, data := range map[string]string{"gregale.yaml": manifest, "package.json": "{}", "Dockerfile": "FROM scratch\n"} {
				if err := os.WriteFile(filepath.Join(source, name), []byte(data), 0600); err != nil {
					t.Fatal(err)
				}
			}
			binary, err := os.Executable()
			if err != nil {
				t.Fatal(err)
			}
			command := func(action, expect string) *exec.Cmd {
				cmd := exec.CommandContext(t.Context(), binary, "-test.run=^TestDeployCronJournalProcessHelper$")
				cmd.Dir = t.TempDir()
				if action == "submit" {
					cmd.Dir = source
				}
				cmd.Env = append(os.Environ(), "GREGALE_CRON_JOURNAL_ACTION="+action, "GREGALE_CRON_JOURNAL_EXPECT="+expect, "GREGALE_CRON_JOURNAL_STATE="+stateDir, "FAAS_API="+srv.URL, "FAAS_TOKEN=fp_journal_fixture")
				return cmd
			}
			child := command("submit", "")
			var output bytes.Buffer
			child.Stdout, child.Stderr = &output, &output
			if err = child.Start(); err != nil {
				t.Fatal(err)
			}
			select {
			case <-entered:
			case <-time.After(20 * time.Second):
				_ = child.Process.Kill()
				_ = child.Wait()
				t.Fatalf("cron stage not reached: %s", &output)
			}
			if err = child.Process.Kill(); err != nil {
				t.Fatal(err)
			}
			_ = child.Wait()
			files, err := filepath.Glob(filepath.Join(stateDir, "gregale", "deploys", "*", "*.json"))
			if err != nil || len(files) != 1 {
				t.Fatalf("saved files=%v err=%v", files, err)
			}
			data, err := os.ReadFile(files[0])
			if err != nil {
				t.Fatal(err)
			}
			var record deploySessionRecord
			if err = json.Unmarshal(data, &record); err != nil {
				t.Fatal(err)
			}
			if record.CronJournal == nil || !record.CronJournal.Managed || record.PolicyJournal.Phase != deployPoliciesPrepared || record.DeploymentID != "" {
				t.Fatalf("journal=%+v", record)
			}
			for _, private := range []string{source, "fp_journal_fixture", "secret-command-sentinel"} {
				if bytes.Contains(data, []byte(private)) {
					t.Fatalf("journal contains private data %q", private)
				}
			}
			info, err := os.Stat(files[0])
			if err != nil || info.Mode().Perm() != 0600 {
				t.Fatalf("permissions=%v err=%v", info, err)
			}
			if strings.HasPrefix(mode, "user-edit") {
				path := "/operator-edit"
				if _, err = f.store.UpdateCron(ctx, record.CronJournal.Intent.Before[0].ID, nil, &path, nil, nil); err != nil {
					t.Fatal(err)
				}
			}
			if err = os.RemoveAll(source); err != nil {
				t.Fatal(err)
			}
			expected := "success"
			if mode == "lost-cleanup-reply" {
				expected = "pending"
			}
			if data, err = command("resume", expected).CombinedOutput(); err != nil {
				t.Fatalf("resume=%v output=%s", err, data)
			}
			if mode == "lost-cleanup-reply" {
				if data, err = command("resume", "success").CombinedOutput(); err != nil {
					t.Fatalf("retry=%v output=%s", err, data)
				}
			}
			if mode == "late-stage" {
				close(release)
				select {
				case err = <-lateResult:
					if !errors.Is(err, state.ErrDeploymentCronConflict) {
						t.Fatalf("late stage=%v", err)
					}
				case <-time.After(5 * time.Second):
					t.Fatal("delayed stage did not finish")
				}
			}
			after, err := f.store.DeploymentCronStagingSnapshot(ctx, f.app.AccountID, f.app.ID)
			if err != nil {
				t.Fatal(err)
			}
			if strings.HasPrefix(mode, "user-edit") {
				if len(after.Crons) != 2 || (after.Crons[0].Path != "/operator-edit" && after.Crons[1].Path != "/operator-edit") {
					t.Fatal("operator edit overwritten")
				}
			} else if !api.DeploymentCronsEqual(after.Crons, before.Crons) {
				t.Fatalf("recovery=%+v before=%+v", after, before)
			}
			if mode == "user-edit-with-policies" {
				app, err := f.store.AppByID(ctx, f.app.ID)
				if err != nil || app.Manifest.Profiling != nil || app.Manifest.Tracing != nil {
					t.Fatalf("cron conflict blocked independent policy cleanup: %+v err=%v", app.Manifest, err)
				}
			}
			all, err := f.store.ListCronsForApp(ctx, f.app.ID)
			if err != nil {
				t.Fatal(err)
			}
			commands := 0
			for _, c := range all {
				if len(c.Command) > 0 {
					commands++
				}
			}
			if commands != 1 {
				t.Fatal("command cron was overwritten")
			}
			f.mu.Lock()
			defer f.mu.Unlock()
			if f.submissions != 0 {
				t.Fatalf("created %d deployments", f.submissions)
			}
		})
	}
}
func TestDeployCronJournalProcessHelper(t *testing.T) {
	action := os.Getenv("GREGALE_CRON_JOURNAL_ACTION")
	if action == "" {
		t.Skip("process helper")
	}
	t.Setenv("XDG_STATE_HOME", os.Getenv("GREGALE_CRON_JOURNAL_STATE"))
	jsonOutput = true
	var output bytes.Buffer
	osStdout, osStderr = &output, &output
	if action == "submit" {
		code := cmdDeployTarballToExisting(t.Context(), []string{"--name", "demo", "--path", ".", "--no-doctor", "--secret-scan", "off"}, true, deployExecution{})
		t.Fatalf("submit returned: code=%d output=%s", code, &output)
	}
	code := cmdDeployResume(t.Context(), []string{"--app", "demo"})
	if os.Getenv("GREGALE_CRON_JOURNAL_EXPECT") == "pending" {
		if code == 0 {
			t.Fatal("pending cleanup reported success")
		}
		return
	}
	if code != 0 {
		t.Fatalf("resume=%d output=%s", code, &output)
	}
	var receipt map[string]any
	if err := json.Unmarshal(output.Bytes(), &receipt); err != nil {
		t.Fatal(err)
	}
	if receipt["cron_cleanup_complete"] != true || receipt["deployment_created"] != false || receipt["submission_outcome"] != "not_submitted" {
		t.Fatalf("receipt=%v", receipt)
	}
}

func TestDeployCronDeferredReceiptAndLegacyJournal(t *testing.T) {
	for _, legacy := range []bool{false, true} {
		t.Run(fmt.Sprint("legacy=", legacy), func(t *testing.T) {
			f := newPolicyJournalFixture(t)
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if !strings.HasSuffix(r.URL.Path, "/deployment-cron-staging") {
					f.serve(w, r)
					return
				}
				if r.Method == "GET" {
					snapshot, err := f.store.DeploymentCronStagingSnapshot(r.Context(), f.app.AccountID, f.app.ID)
					if err != nil {
						t.Error(err)
						w.WriteHeader(503)
						return
					}
					_ = json.NewEncoder(w).Encode(snapshot)
					return
				}
				var request api.DeploymentCronStagingRequest
				if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
					t.Error(err)
					w.WriteHeader(400)
					return
				}
				result, err := f.store.ApplyDeploymentCronStaging(r.Context(), f.app.ID, state.DeploymentCronStagingParams{AccountID: f.app.AccountID, Request: request, Limits: api.MustLimitsFor(api.PlanPro)})
				if err != nil {
					t.Error(err)
					w.WriteHeader(409)
					return
				}
				_ = json.NewEncoder(w).Encode(result.Receipt)
			}))
			defer srv.Close()
			t.Setenv("FAAS_API", srv.URL)
			t.Setenv("FAAS_TOKEN", "fp_journal_fixture")
			t.Setenv("XDG_STATE_HOME", t.TempDir())
			c, err := authedClient()
			if err != nil {
				t.Fatal(err)
			}
			dir := t.TempDir()
			if err = os.WriteFile(filepath.Join(dir, "gregale.yaml"), []byte(cronJournalManifest), 0600); err != nil {
				t.Fatal(err)
			}
			s, lock, err := newSavedDeploySession(t.Context(), c, "demo", f.app.ID, dir, "intent", nil, false, false)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = lock.Close() }()
			if err = s.preparePolicyJournal(t.Context(), c, dir); err != nil {
				t.Fatal(err)
			}
			if err = s.prepareCronJournal(t.Context(), c, dir, false); err != nil {
				t.Fatal(err)
			}
			if !s.record.CronJournal.Deferred {
				t.Fatal("new deploy did not opt into deferred activation")
			}
			if legacy {
				s.record.CronJournal.Deferred = false
			}
			if err = s.stageCrons(t.Context(), c); err != nil {
				t.Fatal(err)
			}
			rows, err := f.store.ListCronsForApp(t.Context(), f.app.ID)
			if err != nil {
				t.Fatal(err)
			}
			if legacy {
				if len(rows) != 1 {
					t.Fatal("legacy journal did not retain stage behavior")
				}
				if err = s.cleanCrons(t.Context(), c); err != nil {
					t.Fatal(err)
				}
				return
			}
			if len(rows) != 0 {
				t.Fatal("prepared crons activated during build")
			}
			var receipt DeployReceipt
			s.decorate(&receipt)
			if receipt.CronActivation != "prepared" || receipt.ConfigurationComplete == nil || *receipt.ConfigurationComplete {
				t.Fatalf("pending receipt=%+v", receipt)
			}
			dep, err := f.store.CreateDeployment(state.ContextWithDeploymentSubmission(t.Context(), s.record.ID), state.Deployment{AppID: f.app.ID, Kind: state.DeploymentKindImage, TrafficPercent: 100})
			if err != nil {
				t.Fatal(err)
			}
			if err = f.store.UpdateDeploymentStatus(t.Context(), dep.ID, state.DeployLive, ""); err != nil {
				t.Fatal(err)
			}
			if changes, err := f.store.ReconcileDeploymentCrons(t.Context()); err != nil || len(changes) != 1 {
				t.Fatalf("autonomous activation=%v err=%v", changes, err)
			}
			if err = s.refreshCronActivation(t.Context(), c); err != nil {
				t.Fatal(err)
			}
			s.decorate(&receipt)
			if receipt.CronActivation != "activated" || receipt.ConfigurationComplete == nil || !*receipt.ConfigurationComplete {
				t.Fatalf("completed receipt=%+v", receipt)
			}
		})
	}
}

func TestTrackedUploadPreventsPrematurePolicyCleanup(t *testing.T) {
	s := &savedDeploySession{record: deploySessionRecord{PolicyJournal: &deployPolicyJournal{Phase: deployPoliciesPrepared}, Outcome: deploySubmissionUnknown, UploadAcceptanceUncertain: true}}
	if s.shouldCleanPolicies() {
		t.Fatal("a tracked upload may already have committed; cleanup needs receipt confirmation")
	}
	s.record.Outcome = deploySubmissionRejected
	if !s.shouldCleanPolicies() {
		t.Fatal("definite rejection should allow cleanup")
	}
	s.record.Outcome = deploySubmissionUnknown
	s.record.UploadAcceptanceUncertain = false
	if !s.shouldCleanPolicies() {
		t.Fatal("an unsubmitted legacy journal should remain recoverable")
	}
}

func TestDeferredUploadResumePrefersAtomicDeploymentReceipt(t *testing.T) {
	f := newPolicyJournalFixture(t)
	uploadReads := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/deployment-submissions/") {
			receipt, err := f.store.DeploymentSubmission(r.Context(), f.app.ID, filepath.Base(r.URL.Path))
			if err != nil {
				t.Error(err)
				w.WriteHeader(404)
				return
			}
			_ = json.NewEncoder(w).Encode(receipt)
			return
		}
		if strings.HasPrefix(r.URL.Path, "/v1/uploads/") {
			uploadReads++
			t.Error("accepted deployment must not depend on upload bookkeeping")
			w.WriteHeader(404)
			return
		}
		f.serve(w, r)
	}))
	defer srv.Close()
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_journal_fixture")
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	c, err := authedClient()
	if err != nil {
		t.Fatal(err)
	}
	s, lock, err := newSavedDeploySession(t.Context(), c, "demo", f.app.ID, "", "intent", nil, false, false)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = lock.Close() }()
	if err = s.preparePolicyJournal(t.Context(), c, ""); err != nil {
		t.Fatal(err)
	}
	s.record.CronJournal = &deployCronJournal{Managed: true, Deferred: true, Intent: api.DeploymentCronStagingRequest{SessionID: s.record.ID, Operation: "stage", ExpectedAppID: f.app.ID, Before: []api.DeploymentCronConfig{}, Desired: []api.DeploymentCronConfig{}}}
	s.record.Upload = &deploySessionUpload{ID: "opaque-unfinished", TotalSize: 1024, ChunkSize: 512}
	s.record.UploadAcceptanceUncertain = true
	dep, err := f.store.CreateDeployment(state.ContextWithDeploymentSubmission(t.Context(), s.record.AttemptID), state.Deployment{AppID: f.app.ID, Kind: state.DeploymentKindImage})
	if err != nil {
		t.Fatal(err)
	}
	if err = s.confirmAcceptance(t.Context(), c); err != nil {
		t.Fatal(err)
	}
	if s.record.DeploymentID != dep.ID || s.record.Outcome != deploySubmissionAccepted || uploadReads != 0 || s.shouldCleanPolicies() {
		t.Fatalf("accepted=%+v upload reads=%d", s.record, uploadReads)
	}
}
