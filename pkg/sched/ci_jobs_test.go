// adr: 1651
// CI scheduling stays default-deny and records output only after native stop.
package sched_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/ci"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/sched"
	"github.com/onebox-faas/faas/pkg/state"
)

type ciSchedulerSource struct{}

func (ciSchedulerSource) Stage(_ context.Context, req ci.JobsSourceRequest) (ci.SourceArchive, error) {
	digest := strings.Repeat("a", 64)
	return ci.SourceArchive{Key: fmt.Sprintf("ci/accounts/%s/executions/%s/attempts/%s/source/%s.tar.gz", req.Key.AccountID, req.Key.ExecutionID, req.Key.AttemptID, digest), Digest: "sha256:" + digest, Bytes: 1}, nil
}

// Boot and release record synthetic native receipts. They never run commands.
type ciSchedulerBoot struct {
	store      *state.PgStore
	spec       sched.JobVmmSpec
	calls      int
	releases   int
	hook       func()
	bootErr    error
	releaseErr error
	ignoreHeld bool
}

func (v *ciSchedulerBoot) JobColdBoot(_ context.Context, spec sched.JobVmmSpec) (sched.JobVmmResult, error) {
	v.spec, v.calls = spec, v.calls+1
	if v.hook != nil {
		v.hook()
	}
	return sched.JobVmmResult{InstanceID: spec.InstanceID, NodeID: spec.NodeID, Netns: "ci-synthetic", HostIP: "10.100.0.25", GuestUID: 20025, StartHeld: spec.StartHeld && !v.ignoreHeld}, v.bootErr
}

func (v *ciSchedulerBoot) ReleaseJobStart(ctx context.Context, spec sched.JobStartSpec) error {
	instance, err := v.store.InstanceByID(ctx, spec.InstanceID)
	if err != nil || instance.State != string(state.StateRunning) || !v.spec.StartHeld {
		return errors.New("release before durable runtime publication")
	}
	v.releases++
	return v.releaseErr
}

type ciSchedulerHarness struct {
	pool    *pgxpool.Pool
	store   *state.PgStore
	ci      *ci.PostgresStore
	cfg     ci.Config
	row     ci.Execution
	backend ci.JobsBackend
	binding ci.GitHubBinding
	engine  *sched.Engine
	boot    *ciSchedulerBoot
	vmm     *sched.FakeVMM
}

func newCISchedulerHarness(t *testing.T) *ciSchedulerHarness {
	t.Helper()
	pool := pgtest.OpenMigrated(t)
	h := &ciSchedulerHarness{pool: pool, store: state.NewPgStore(pool), ci: ci.NewPostgresStore(pool), vmm: &sched.FakeVMM{}}
	ctx := t.Context()
	account, err := h.store.CreateAccount(ctx, "ci-scheduler-"+uuid.NewString()+"@example.invalid", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	image := "registry.example/ci@sha256:" + strings.Repeat("a", 64)
	job, err := h.store.JobCreate(ctx, account.ID, "ci-scheduler", "batch", image, []string{ci.JobsRunnerPath, ci.JobsRunnerProtocol}, api.CIJobsRAMMB, 60, 1, 0, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, "UPDATE jobs SET image_materialization_status='ready',image_resolved_digest=$2,image_storage_key=$3 WHERE id=$1", job.ID, "sha256:"+strings.Repeat("a", 64), "jobs/"+job.ID+"/image.ext4"); err != nil {
		t.Fatal(err)
	}
	revision, err := h.ci.PutRevision(ctx, account.ID, "checks", ci.Definition{Version: 1, Repository: "gregale/scheduler", Jobs: map[string]ci.Job{"test": {Image: image, TimeoutSeconds: 60, Steps: []ci.Step{{Name: "unit", Run: "synthetic step; never executed"}}}}})
	if err != nil {
		t.Fatal(err)
	}
	h.binding, err = h.ci.PutGitHubBinding(ctx, account.ID, "source", ci.GitHubBindingRequest{RevisionID: revision.ID, InstallationID: 12, RepositoryID: 34, PushBranches: []string{"main"}})
	if err != nil {
		t.Fatal(err)
	}
	h.cfg = ci.ConfigFromEnv(func(name string) string {
		switch name {
		case ci.InternalEnv, ci.GitHubEnv, ci.DispatchEnv, ci.TransportEnv:
			return "1"
		case ci.AccountsEnv:
			return account.ID
		}
		return ""
	})
	verify := func(context.Context, string, int64, int64, string) error { return nil }
	h.row, _, err = ci.AdmitJobs(ctx, h.cfg, h.ci, verify, account.ID, "fresh", ci.JobsSubmitRequest{SubmitRequest: ci.SubmitRequest{RevisionID: revision.ID, CommitSHA: strings.Repeat("a", 40)}, BindingID: h.binding.ID, Templates: map[string]string{"test": job.ID}})
	if err != nil {
		t.Fatal(err)
	}
	h.backend = ci.JobsBackend{Config: h.cfg, Store: h.ci, Source: ciSchedulerSource{}, Output: ci.SealedJobsOutput{Store: h.ci}}
	h.row, err = (ci.Engine{Store: h.ci, Backend: h.backend}).Tick(ctx, account.ID, h.row.ID)
	if err != nil {
		t.Fatal(err)
	}
	h.boot = &ciSchedulerBoot{store: h.store}
	h.engine = sched.XNewEngine(t, h.store, h.vmm, &sched.FakeNotifier{}, "1.10.0").
		WithJobLeaser(sched.AdaptJobLeaser(sched.NewMemLeaser(nil))).WithJobVmmClient(h.boot).
		WithCIJobsRuntime(h.ci, h.cfg, "https://ci-runtime.example.invalid")
	return h
}

func (h *ciSchedulerHarness) wake(t *testing.T) (sched.JobWakeResult, error) {
	t.Helper()
	return h.engine.WakeJob(t.Context(), h.row.AccountID, h.row.Jobs[0].Attempt.ID, 0)
}

func TestCIJobsSchedulerIntegration(t *testing.T) {
	probe := pgtest.Open(t)
	probe.Close()
	if os.Getenv(pgtest.UseTemplateDatabase) == "" {
		t.Setenv(pgtest.UseTemplateDatabase, "1")
	}
	t.Run("held_start_grant_and_restart", func(t *testing.T) {
		h := newCISchedulerHarness(t)
		h.boot.hook = func() {
			spec := h.boot.spec
			proof := ci.RuntimeProof{RunID: spec.RunID, InstanceID: spec.InstanceID, Capability: spec.Env["GREGALE_CI_CAPABILITY"]}
			if _, err := h.ci.AuthorizeJobsRuntime(t.Context(), h.cfg, proof, ci.RuntimeSource); !errors.Is(err, ci.ErrRuntimeAuthority) {
				t.Fatal("source was released before native boot identity was published")
			}
		}
		wake, err := h.wake(t)
		if err != nil {
			t.Fatal(err)
		}
		spec := h.boot.spec
		proof := ci.RuntimeProof{RunID: wake.RunID, InstanceID: wake.InstanceID, Capability: spec.Env["GREGALE_CI_CAPABILITY"]}
		if !spec.StartHeld || h.boot.releases != 1 || !ci.ValidRuntimeProof(proof) || proof.Capability == spec.LeaseToken || spec.TaskTimeoutSec > 60 || spec.TaskTimeoutSec < 1 {
			t.Fatal("CI task omitted its held start, deadline or scoped proof")
		}
		if _, err := h.ci.AuthorizeJobsRuntime(t.Context(), h.cfg, proof, ci.RuntimeSource); err != nil {
			t.Fatal(err)
		}
		h.engine = sched.XNewEngine(t, h.store, h.vmm, &sched.FakeNotifier{}, "1.10.0").WithJobVmmClient(h.boot).
			WithCIJobsRuntime(h.ci, h.cfg, "https://ci-runtime.example.invalid")
		if _, err := h.wake(t); !errors.Is(err, sched.ErrJobTaskAlreadyClaimed) || h.boot.calls != 1 {
			t.Fatal("scheduler restart executed the same CI task again")
		}
		if err := h.engine.HandleJobExit(t.Context(), wake.RunID /* wrong account is refused */, wake.RunID, 0, 0, "succeeded", string(wake.LeaseToken)); err == nil {
			t.Fatal("cross-account native completion accepted")
		}
		if err := h.engine.HandleJobExit(t.Context(), h.row.AccountID, wake.RunID, 0, 0, "succeeded", string(wake.LeaseToken)); err != nil {
			t.Fatal(err)
		}
		row, err := (ci.Engine{Store: h.ci, Backend: h.backend}).Tick(t.Context(), h.row.AccountID, h.row.ID)
		if err != nil || row.Status != ci.StatusFailed || !strings.Contains(string(row.Jobs[0].Attempt.Logs), "incomplete") {
			t.Fatalf("unsealed native success did not become platform failure: %v", err)
		}
	})
	t.Run("default_off_and_missing_origin_never_boot", func(t *testing.T) {
		h := newCISchedulerHarness(t)
		h.engine = sched.XNewEngine(t, h.store, h.vmm, &sched.FakeNotifier{}, "1.10.0").WithJobVmmClient(h.boot)
		if _, err := h.wake(t); !errors.Is(err, ci.ErrRuntimeAuthority) {
			t.Fatal("unconfigured scheduler accepted owned CI")
		}
		h.engine.WithCIJobsRuntime(h.ci, h.cfg, "")
		if _, err := h.wake(t); !errors.Is(err, ci.ErrRuntimeAuthority) || h.boot.calls != 0 {
			t.Fatal("missing transport origin admitted compute")
		}
		h.engine.WithCIJobsRuntime(h.ci, h.cfg, "https://ci-runtime.example.invalid").WithJobVmmClient(&sched.RecordingJobVMM{})
		if _, err := h.wake(t); !errors.Is(err, ci.ErrRuntimeAuthority) {
			t.Fatal("missing held-start release capability admitted compute")
		}
		var count int
		if err := h.pool.QueryRow(t.Context(), "SELECT count(*) FROM instances WHERE kind='job_task'").Scan(&count); err != nil || count != 0 {
			t.Fatal("refused CI consumed a native instance")
		}
	})
	for _, name := range []string{"cancel_during_boot", "binding_revoked_during_boot", "destroy_failure_retains_capacity", "boot_failure", "lost_release_ack", "held_start_unsupported"} {
		t.Run(name, func(t *testing.T) {
			h := newCISchedulerHarness(t)
			h.boot.hook = func() {
				if name == "cancel_during_boot" {
					if _, err := h.ci.CancelExecution(t.Context(), h.row.AccountID, h.row.ID); err != nil {
						t.Fatal(err)
					}
				} else if name != "lost_release_ack" && name != "boot_failure" && name != "held_start_unsupported" {
					if _, err := h.pool.Exec(t.Context(), "UPDATE ci_github_bindings SET enabled=false WHERE id=$1", h.binding.ID); err != nil {
						t.Fatal(err)
					}
				}
			}
			if name == "destroy_failure_retains_capacity" {
				(*h.vmm.DestroyErr()) = errors.New("synthetic destroy failure")
			}
			if name == "boot_failure" {
				h.boot.bootErr = errors.New("synthetic boot failure")
			}
			if name == "lost_release_ack" {
				h.boot.releaseErr = errors.New("synthetic lost response")
			}
			if name == "held_start_unsupported" {
				h.boot.ignoreHeld = true
			}
			_, err := h.wake(t)
			task, readErr := h.store.JobTaskGet(t.Context(), h.row.Jobs[0].Attempt.ID, 0)
			if readErr != nil || task.InstanceID == nil {
				t.Fatal("lost bound native task")
			}
			instance, readErr := h.store.InstanceByID(t.Context(), *task.InstanceID)
			if readErr != nil {
				t.Fatal(readErr)
			}
			if name == "lost_release_ack" {
				if err != nil || h.boot.releases != 1 || task.Status != "claimed" || task.Attempt != 1 {
					t.Fatal("ambiguous release re-executed or discarded a live task")
				}
				return
			}
			if err == nil || task.Status != "failed" || task.Attempt != 1 || h.boot.releases != 0 {
				t.Fatal("revoked/failed boot released the command or retried")
			}
			if name == "destroy_failure_retains_capacity" {
				if instance.State == string(state.StateStopped) || !(*h.engine.XLedger()).ResidentFor(instance.ID) {
					t.Fatal("failed destroy certified cleanup or released capacity")
				}
				(*h.vmm.DestroyErr()) = nil
				if _, err := h.engine.ReconcileOrphanedJobInstances(t.Context(), 10); err != nil {
					t.Fatal(err)
				}
				instance, err = h.store.InstanceByID(t.Context(), instance.ID)
				if err != nil || instance.State != string(state.StateStopped) || (*h.engine.XLedger()).ResidentFor(instance.ID) {
					t.Fatal("native reconciliation did not finish retained cleanup")
				}
			} else if instance.State != string(state.StateStopped) || (*h.vmm.Destroys()) != 1 {
				t.Fatal("CI rollback did not confirm native cleanup")
			}
		})
	}
	t.Run("store_blocks_infrastructure_retries", func(t *testing.T) {
		for _, reaper := range []bool{false, true} {
			h := newCISchedulerHarness(t)
			wake, err := h.wake(t)
			if err != nil {
				t.Fatal(err)
			}
			var retried bool
			if reaper {
				if _, err := h.pool.Exec(t.Context(), "UPDATE job_tasks SET lease_expires_at=clock_timestamp()-interval '1 hour' WHERE run_id=$1", wake.RunID); err != nil {
					t.Fatal(err)
				}
				retried, err = h.store.JobTaskReapClaimed(t.Context(), wake.RunID, 0, string(wake.LeaseToken), time.Now(), 10, time.Now())
			} else {
				retried, err = h.store.JobTaskFailBoot(t.Context(), wake.RunID, 0, wake.InstanceID, string(wake.LeaseToken), 10, time.Now(), "synthetic infrastructure failure")
			}
			if err != nil || retried {
				t.Fatalf("store retried an owned CI command: %v", err)
			}
			task, err := h.store.JobTaskGet(t.Context(), wake.RunID, 0)
			if err != nil || task.Attempt != 1 || task.Status == "queued" {
				t.Fatal("CI infrastructure failure acquired another attempt")
			}
			h.engine.CleanupJobInstance(t.Context(), wake.InstanceID, wake.NodeID, "synthetic_ci_cleanup")
		}
	})
}

func TestCIJobReservedEnvIsRemoved(t *testing.T) {
	env := map[string]string{"GREGALE_CI_CAPABILITY": "forged", "GREGALE_CI_RUNTIME_URL": "https://forged.invalid", "USER_VALUE": "kept"}
	sched.ScrubCIJobEnv(env)
	if len(env) != 1 || env["USER_VALUE"] != "kept" {
		t.Fatal("customer CI runtime authority was forwarded")
	}
}
