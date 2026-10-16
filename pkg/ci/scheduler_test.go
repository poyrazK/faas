package ci

import (
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
)

func TestCIJobsSchedulerPolicy(t *testing.T) {
	probe := pgtest.Open(t)
	probe.Close()
	if os.Getenv(pgtest.UseTemplateDatabase) == "" {
		t.Setenv(pgtest.UseTemplateDatabase, "1")
	}
	for _, name := range []string{"valid", "flags_off", "wrong_account", "wrong_partition", "free_plan", "abuse_hold", "binding_disabled", "template_changed", "command_changed", "environment_changed", "retry_changed", "deadline"} {
		t.Run(name, func(t *testing.T) {
			h := newJobsHarness(t)
			row := h.tick(t, h.admit(t, "launch"))
			run := row.Jobs[0].Attempt.ID
			cfg, account, index := h.backend.Config, h.account, 0
			var err error
			switch name {
			case "flags_off":
				cfg = Config{}
			case "wrong_account":
				account = h.other
			case "wrong_partition":
				index = 1
			case "free_plan":
				_, err = h.pool.Exec(t.Context(), "UPDATE accounts SET plan='free' WHERE id=$1", h.account)
			case "abuse_hold":
				_, err = h.pool.Exec(t.Context(), "UPDATE accounts SET abuse_hold_at=clock_timestamp(),abuse_hold_reason='egress_flood' WHERE id=$1", h.account)
			case "binding_disabled":
				_, err = h.pool.Exec(t.Context(), "UPDATE ci_github_bindings SET enabled=false WHERE id=$1", h.binding.ID)
			case "template_changed":
				_, err = h.pool.Exec(t.Context(), "UPDATE jobs SET image_storage_key='different' WHERE id=$1", h.req.Templates["test"])
			case "command_changed":
				_, err = h.pool.Exec(t.Context(), "UPDATE job_runs SET command=ARRAY['untrusted'] WHERE id=$1", run)
			case "environment_changed":
				_, err = h.pool.Exec(t.Context(), `UPDATE job_runs SET effective_env_snapshot='{"INJECTED":"refused"}' WHERE id=$1`, run)
			case "retry_changed":
				_, err = h.pool.Exec(t.Context(), "UPDATE job_runs SET retry_max=4 WHERE id=$1", run)
			case "deadline":
				h.clock.advance(601)
			}
			if err != nil {
				t.Fatal(err)
			}
			owned, deadline, err := h.store.ValidateJobsLaunch(t.Context(), cfg, account, run, index)
			if name == "valid" {
				if err != nil || !owned || !deadline.Equal(row.Jobs[0].Attempt.Deadline) {
					t.Fatalf("valid launch refused: %v", err)
				}
			} else if !owned || !errors.Is(err, ErrRuntimeAuthority) {
				t.Fatalf("invalid launch accepted: owned=%t err=%v", owned, err)
			}
			if owned, _, err := h.store.ValidateJobsLaunch(t.Context(), cfg, account, uuid.NewString(), 0); err != nil || owned {
				t.Fatal("ordinary Jobs run acquired CI ownership")
			}
		})
	}
}

func TestCIJobsStoppedWithoutSealFailsDurably(t *testing.T) {
	probe := pgtest.Open(t)
	probe.Close()
	if os.Getenv(pgtest.UseTemplateDatabase) == "" {
		t.Setenv(pgtest.UseTemplateDatabase, "1")
	}
	for _, native := range []string{StatusSucceeded, StatusFailed} {
		t.Run(native, func(t *testing.T) {
			h, row, _, _ := runtimeFixture(t)
			h.backend.Output = SealedJobsOutput{Store: h.store, Blobs: h.blobs}
			code := 0
			if native == StatusFailed {
				code = 75
			}
			h.publish(t, row, "test", native, code, nil, true, false)
			row = h.tick(t, row)
			if terminal(row.Jobs[0].Status) {
				t.Fatal("unconfirmed VM stop certified a platform result")
			}
			if _, err := h.pool.Exec(t.Context(), "UPDATE instances SET state='stopped' WHERE id=(SELECT instance_id FROM job_tasks WHERE run_id=$1)", row.Jobs[0].Attempt.ID); err != nil {
				t.Fatal(err)
			}
			row = h.tick(t, row)
			if row.Jobs[0].Status != StatusFailed || !strings.Contains(string(row.Jobs[0].Attempt.Logs), "incomplete") {
				t.Fatal("missing seal did not settle as a platform failure")
			}
			h.restart()
			row = h.tick(t, row)
			if row.Jobs[0].Status != StatusFailed || *row.Jobs[0].Attempt.ExitCode == 0 {
				t.Fatal("platform failure did not survive controller restart")
			}
		})
	}
}
