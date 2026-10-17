// ADR-1655: saved profiles pin source/image authority and cannot enable compute.
package ci

import (
	"context"
	"errors"
	"reflect"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

func (h *jobsHarness) profileRequest() ExecutionProfileRequest {
	return ExecutionProfileRequest{RevisionID: h.req.RevisionID, BindingID: h.req.BindingID, Templates: clone(h.req.Templates)}
}

func (h *jobsHarness) profile(t *testing.T, name string) ExecutionProfile {
	t.Helper()
	// Setup must work with every compute-specific flag disabled.
	cfg := h.backend.Config
	cfg.DispatchEnabled, cfg.WorkerEnabled, cfg.TransportEnabled = false, false, false
	profile, err := SaveExecutionProfile(t.Context(), cfg, h.store, h.verify, h.account, name, h.profileRequest())
	if err != nil {
		t.Fatal(err)
	}
	return profile
}

func TestCIProfileImmutableRestartAndAdmission(t *testing.T) {
	h := newJobsHarness(t)
	profile := h.profile(t, "checks")
	if profile.BindingGeneration != h.binding.Generation || profile.RevisionID != h.req.RevisionID || !reflect.DeepEqual(profile.Templates, h.req.Templates) {
		t.Fatal("profile lost its immutable configuration")
	}
	if h.count(t, "SELECT count(*) FROM ci_executions") != 0 || h.count(t, "SELECT count(*) FROM job_runs") != 0 || h.count(t, "SELECT count(*) FROM instances") != 0 {
		t.Fatal("saving a profile admitted compute intent")
	}
	if replay := h.profile(t, "checks"); replay.ID != profile.ID {
		t.Fatal("repeated profile PUT created a different identity")
	}
	changed := h.profileRequest()
	changed.Templates["test"] = uuid.NewString()
	if _, err := SaveExecutionProfile(t.Context(), h.backend.Config, h.store, h.verify, h.account, "checks", changed); !errors.Is(err, ErrConflict) {
		t.Fatal("profile configuration was mutable")
	}
	req := JobsSubmitRequest{ProfileID: profile.ID, SubmitRequest: SubmitRequest{CommitSHA: h.req.CommitSHA}}
	row, replay, err := AdmitJobs(t.Context(), h.backend.Config, h.store, h.verify, h.account, "profile-first", req)
	if err != nil || replay || row.ProfileID != profile.ID || row.RevisionID != profile.RevisionID || len(row.Compute.Templates) != len(profile.Templates) {
		t.Fatalf("profile admission: replay=%v err=%v", replay, err)
	}
	h.restart()
	again, replay, err := AdmitJobs(t.Context(), h.backend.Config, h.store, h.verify, h.account, "profile-first", req)
	if err != nil || !replay || again.ID != row.ID {
		t.Fatal("profile admission lost restart-safe idempotency")
	}
	if _, _, err := AdmitJobs(t.Context(), h.backend.Config, h.store, h.verify, h.account, "profile-first", h.req); !errors.Is(err, ErrConflict) {
		t.Fatal("manual submission reused a profile's request identity")
	}
	if _, err := h.store.ExecutionProfile(t.Context(), h.other, profile.ID); !errors.Is(err, ErrNotFound) {
		t.Fatal("profile crossed account ownership")
	}
	if _, err := h.store.DisableExecutionProfile(t.Context(), h.other, profile.ID); !errors.Is(err, ErrNotFound) {
		t.Fatal("foreign account disabled a profile")
	}
	for range 2 {
		disabled, err := h.store.DisableExecutionProfile(t.Context(), h.account, profile.ID)
		if err != nil || disabled.State != ProfileDisabled {
			t.Fatal("profile disable was not idempotent")
		}
	}
	if _, _, err := AdmitJobs(t.Context(), h.backend.Config, h.store, h.verify, h.account, "after-disable", req); !errors.Is(err, ErrConflict) {
		t.Fatal("disabled profile admitted fresh execution")
	}
	if saved := h.profile(t, "checks"); saved.State != ProfileDisabled {
		t.Fatal("repeated PUT re-enabled a disabled profile")
	}
	if current, err := h.store.Execution(t.Context(), h.account, row.ID); err != nil || current.Status != StatusQueued {
		t.Fatal("profile disable changed existing execution")
	}
	if _, err := h.store.CancelExecution(t.Context(), h.account, row.ID); err != nil {
		t.Fatal("profile disable prevented execution cancellation")
	}
}

func TestCIProfileRejectsInvalidOrForeignTemplates(t *testing.T) {
	for _, name := range []string{"foreign", "missing", "extra", "wrong-image", "unready", "environment", "invalid-name", "revision-digest"} {
		t.Run(name, func(t *testing.T) {
			h := newJobsHarness(t)
			req := h.profileRequest()
			var query string
			switch name {
			case "foreign":
				if _, err := h.pool.Exec(t.Context(), "UPDATE jobs SET account_id=$2 WHERE id=$1", req.Templates["test"], h.other); err != nil {
					t.Fatal(err)
				}
			case "missing":
				delete(req.Templates, "test")
			case "extra":
				req.Templates["extra"] = uuid.NewString()
			case "wrong-image":
				query = "UPDATE jobs SET image_ref='registry.example/other@sha256:' || repeat('a',64) WHERE id=$1"
			case "unready":
				query = "UPDATE jobs SET image_materialization_status='pending' WHERE id=$1"
			case "environment":
				query = "UPDATE jobs SET env_overrides='{\"UNTRUSTED\":\"value\"}' WHERE id=$1"
			case "invalid-name":
				req.Templates["../outside"] = req.Templates["test"]
			case "revision-digest":
				if _, err := h.pool.Exec(t.Context(), "UPDATE ci_pipeline_revisions SET digest='sha256:' || repeat('c',64) WHERE id=$1", req.RevisionID); err != nil {
					t.Fatal(err)
				}
			}
			if query != "" {
				if _, err := h.pool.Exec(t.Context(), query, req.Templates["test"]); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := SaveExecutionProfile(t.Context(), h.backend.Config, h.store, h.verify, h.account, "invalid", req); err == nil {
				t.Fatal("unsafe profile was accepted")
			}
			if h.count(t, "SELECT count(*) FROM ci_execution_profiles") != 0 {
				t.Fatal("invalid profile retained partial configuration")
			}
		})
	}
}

func TestCIProfileAdmissionRechecksFrozenAuthority(t *testing.T) {
	for _, name := range []string{"binding-generation", "materialized-image", "disable-during-verification", "override", "source-access"} {
		t.Run(name, func(t *testing.T) {
			h := newJobsHarness(t)
			profile := h.profile(t, "checks")
			req := JobsSubmitRequest{ProfileID: profile.ID, SubmitRequest: SubmitRequest{CommitSHA: h.req.CommitSHA}}
			verify := h.verify
			switch name {
			case "binding-generation":
				changed := h.binding.GitHubBindingRequest
				changed.PushBranches = []string{"release"}
				if _, err := h.store.PutGitHubBinding(t.Context(), h.account, h.binding.Name, changed); err != nil {
					t.Fatal(err)
				}
			case "materialized-image":
				if _, err := h.pool.Exec(t.Context(), "UPDATE jobs SET image_storage_key='replacement/rootfs.ext4' WHERE id=$1", h.req.Templates["test"]); err != nil {
					t.Fatal(err)
				}
			case "disable-during-verification":
				verify = func(ctx context.Context, _ string, _, _ int64, _ string) error {
					_, err := h.store.DisableExecutionProfile(ctx, h.account, profile.ID)
					return err
				}
			case "override":
				req.Templates = clone(h.req.Templates)
			case "source-access":
				verify = func(context.Context, string, int64, int64, string) error { return ErrGitHubAccess }
			}
			if _, _, err := AdmitJobs(t.Context(), h.backend.Config, h.store, verify, h.account, "reject", req); err == nil {
				t.Fatal("stale or changed profile authority admitted execution")
			}
			if h.count(t, "SELECT count(*) FROM ci_executions") != 0 {
				t.Fatal("rejected profile created execution intent")
			}
		})
	}
}

func TestCIProfileQuotaSerializesAndRetainsDisabledRows(t *testing.T) {
	h := newJobsHarness(t)
	profile := h.profile(t, "checks")
	if _, err := h.pool.Exec(t.Context(), `INSERT INTO ci_execution_profiles(id,account_id,name,revision_id,revision_digest,binding_id,binding_generation,templates,snapshot)
 SELECT gen_random_uuid(),account_id,'seed-' || n,revision_id,revision_digest,binding_id,binding_generation,templates,snapshot
 FROM ci_execution_profiles CROSS JOIN generate_series(1,$2::integer) n WHERE id=$1`, profile.ID, api.CIMaxExecutionProfilesPerAccount-2); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for _, name := range []string{"last-a", "last-b"} {
		wg.Go(func() {
			_, err := SaveExecutionProfile(t.Context(), h.backend.Config, h.store, h.verify, h.account, name, h.profileRequest())
			results <- err
		})
	}
	wg.Wait()
	close(results)
	success, full := 0, 0
	for err := range results {
		if err == nil {
			success++
		} else if errors.Is(err, ErrLimit) {
			full++
		} else {
			t.Fatal(err)
		}
	}
	if success != 1 || full != 1 || h.count(t, "SELECT count(*) FROM ci_execution_profiles") != api.CIMaxExecutionProfilesPerAccount {
		t.Fatal("concurrent profile writes escaped the account quota")
	}
	if saved := h.profile(t, "checks"); saved.ID != profile.ID {
		t.Fatal("quota blocked existing profile replay")
	}
	if _, err := h.store.DisableExecutionProfile(t.Context(), h.account, profile.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := SaveExecutionProfile(t.Context(), h.backend.Config, h.store, h.verify, h.account, "full", h.profileRequest()); !errors.Is(err, ErrLimit) {
		t.Fatal("disabled profile stopped counting toward retained quota")
	}
}
