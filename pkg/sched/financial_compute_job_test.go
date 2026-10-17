package sched

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/financial"
	"github.com/onebox-faas/faas/pkg/grpcerr"
	"github.com/onebox-faas/faas/pkg/runtimeadmission"
	"github.com/onebox-faas/faas/pkg/state"
)

type financialJobVMM struct {
	store       *state.MemStore
	incarnation string
	calls       int
}

func (v *financialJobVMM) JobColdBoot(ctx context.Context, s JobVmmSpec) (JobVmmResult, error) {
	v.calls++
	_, err := v.store.AcquireFinancialCompute(ctx, financial.ComputeRequest{AccountID: s.AccountID, JobID: s.JobID, JobRunID: s.RunID, JobTaskIndex: s.TaskIndex, InstanceID: s.InstanceID, NodeID: s.NodeID, Incarnation: v.incarnation, AttemptID: financial.ComputeJobAttemptID(s.RunID, s.TaskIndex, s.LeaseToken), Fenced: true, BillableMB: api.BillableRAMMB(s.RAMMB)})
	return JobVmmResult{}, err
}
func (*financialJobVMM) WaitJobExit(context.Context, JobExitSpec) (JobExitResult, error) {
	return JobExitResult{}, errors.New("denied job must not wait for an exit")
}

// adr: 1641 — a refusal to fund a boot preserves the queued task and retry
// count, but uncertain native cleanup retains the original physical claim.
func TestFinancialComputeJobBudgetDeferral(t *testing.T) {
	for _, failedDestroy := range []bool{false, true} {
		name := "queued_without_retry"
		if failedDestroy {
			name = "retain_uncertain_claim"
		}
		t.Run(name, func(t *testing.T) {
			store := state.NewMemStore()
			account, job, run := seedJobRun(t, store, nil, nil)
			nodes, err := store.ListComputeNodes(t.Context(), false)
			if err != nil {
				t.Fatal(err)
			}
			var incarnation string
			for _, n := range nodes {
				incarnation = uuid.NewString()
				if err := store.RegisterComputeNodeRuntimeIdentity(t.Context(), runtimeadmission.Identity{NodeID: n.ID, Incarnation: incarnation, ProtocolVersion: 1}); err != nil {
					t.Fatal(err)
				}
				if err := store.RegisterFinancialComputeOwner(t.Context(), n.ID, incarnation); err != nil {
					t.Fatal(err)
				}
			}
			for at := job.CreatedAt.Truncate(time.Minute); !at.After(time.Now().UTC().Truncate(time.Minute)); at = at.Add(time.Minute) {
				if err := store.RecordFinancialSamplingWindow(t.Context(), at, true, false); err != nil {
					t.Fatal(err)
				}
			}
			p, err := store.CreateFinancialBudget(t.Context(), account.ID, uuid.NewString(), "test", financial.BudgetSpec{Name: "job cap", Scope: financial.BudgetScope{Kind: "job", ID: job.ID}, Currency: "USD", Meters: []string{"compute"}, Basis: "gross_usage", LimitMillicents: 0, Mode: "strict", Action: "suspend_workloads", ResumeRule: "manual", Enabled: true})
			if err != nil {
				t.Fatal(err)
			}
			native := &fakeVMM{}
			if failedDestroy {
				native.destroyErr = errors.New("owner unavailable")
			}
			jobVMM := &financialJobVMM{store: store, incarnation: incarnation}
			e := newEngine(t, store, native, &fakeNotifier{}, "1.10.0").WithJobLeaser(AdaptJobLeaser(NewMemLeaser(nil))).WithJobVmmClient(jobVMM)
			before, err := store.JobTaskGet(t.Context(), run.ID, 0)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := e.WakeJob(t.Context(), account.ID, run.ID, 0); !errors.Is(err, financial.ErrComputeBudget) {
				t.Fatalf("budget refusal: %v", err)
			}
			after, err := store.JobTaskGet(t.Context(), run.ID, 0)
			if err != nil {
				t.Fatal(err)
			}
			if after.Attempt != before.Attempt {
				t.Fatal("budget refusal consumed an execution retry")
			}
			if failedDestroy {
				if after.Status != "claimed" || after.InstanceID == nil || !e.ledger.ResidentFor(*after.InstanceID) {
					t.Fatalf("uncertain claim lost: %+v", after)
				}
			} else {
				if after.Status != "queued" || after.InstanceID != nil || after.NextAttemptAt == nil {
					t.Fatalf("task not deferred: %+v", after)
				}
				if _, err := e.WakeJob(t.Context(), account.ID, run.ID, 0); err == nil {
					t.Fatal("held queued task admitted")
				}
				if jobVMM.calls != 1 {
					t.Fatal("held task reached native dispatch")
				}
			}
			b, err := store.FinancialComputeBalance(t.Context(), p, time.Now())
			if err != nil || !b.Held || b.CommittedMillicents != 0 || b.ReservedMillicents != 0 {
				t.Fatalf("denial accounting: %+v %v", b, err)
			}
		})
	}
}

func TestFinancialJobAuthorityUnavailablePreservesDeferralClassification(t *testing.T) {
	for _, err := range []error{
		financial.ErrComputeUnavailable,
		financial.ErrComputeFence,
		api.NewProblem(503, "financial_compute_unavailable", "unavailable", "funding could not be checked"),
		grpcerr.ToStatus(api.NewProblem(503, "financial_compute_unavailable", "unavailable", "funding could not be checked")),
	} {
		if !financialJobAuthorityUnavailable(err) {
			t.Fatalf("financial infrastructure error consumed execution retry: %v", err)
		}
	}
	for _, err := range []error{errors.New("guest command failed"), api.NewProblem(503, "capacity", "capacity", "RAM exhausted")} {
		if financialJobAuthorityUnavailable(err) {
			t.Fatalf("unrelated execution failure was deferred as financial: %v", err)
		}
	}
}
