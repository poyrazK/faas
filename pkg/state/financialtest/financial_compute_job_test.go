package financialtest

import (
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/financial"
	"github.com/onebox-faas/faas/pkg/state"
)

// adr: 1641 — task claims, funding and native retirement have separate owners.
func TestFinancialComputeJobs(t *testing.T) {
	for _, backend := range []string{"memory", "postgres"} {
		t.Run(backend, func(t *testing.T) {
			var store computeTestStore = state.NewMemStore()
			if backend == "postgres" {
				store, _, _ = financialPostgres(t)
			}
			for _, kind := range []string{"batch", "recurring", "image_run"} {
				t.Run(kind, func(t *testing.T) {
					f := newAccountComputeFixture(t, store, 1000000, 1000)
					job, run := computeJob(t, f, kind)
					r := claimComputeJob(t, f, job, run, 0)
					for _, change := range []func(*financial.ComputeRequest){
						func(r *financial.ComputeRequest) { r.BillableMB-- },
						func(r *financial.ComputeRequest) { r.JobID = uuid.NewString() },
						func(r *financial.ComputeRequest) { r.JobRunID = uuid.NewString() },
						func(r *financial.ComputeRequest) { r.JobTaskIndex++ },
						func(r *financial.ComputeRequest) { r.AttemptID = uuid.NewString() },
						func(r *financial.ComputeRequest) { r.NodeID = f.owners[1].NodeID },
					} {
						bad := r
						change(&bad)
						if _, err := store.AcquireFinancialCompute(t.Context(), bad); !errors.Is(err, financial.ErrComputeFence) {
							t.Fatalf("spoofed claim: %v", err)
						}
					}
					l, err := store.AcquireFinancialCompute(t.Context(), r)
					if err != nil {
						t.Fatal(err)
					}
					if !l.AccountFunded || !l.Strict || l.JobID != job.ID || l.AppID != "" || l.JobRunID != run.ID || l.QuantityReserved != int64(136)*150 {
						t.Fatalf("job funding: %+v", l)
					}
					before := f.balance(t)
					again, err := store.AcquireFinancialCompute(t.Context(), r)
					if err != nil || again.ID != l.ID || again.Revision != l.Revision || f.balance(t).AllowanceReserved != before.AllowanceReserved {
						t.Fatalf("lost grant reply replay: %+v %v", again, err)
					}
					if _, err := store.JobRunCancel(t.Context(), run.ID); err != nil {
						t.Fatal(err)
					}
					if _, err := store.RenewFinancialCompute(t.Context(), jobComputeMutation(l)); !errors.Is(err, financial.ErrComputeFence) {
						t.Fatalf("cancelled run renewed: %v", err)
					}
					if f.balance(t).AllowanceReserved != before.AllowanceReserved {
						t.Fatal("cancellation freed unconfirmed physical funding")
					}
					settled, err := store.SettleFinancialCompute(t.Context(), jobComputeMutation(l))
					if err != nil || settled.QuantityReserved != 0 || settled.Reserved != 0 || f.balance(t).AllowanceReserved != 0 {
						t.Fatalf("physical settlement: %+v %v", settled, err)
					}
					if _, err := store.AcquireFinancialCompute(t.Context(), r); !errors.Is(err, financial.ErrComputeFence) {
						t.Fatalf("late cancelled boot escaped: %v", err)
					}
				})
			}
			t.Run("apps_and_jobs_contend_for_one_allowance", func(t *testing.T) {
				f := newAccountComputeFixture(t, store, 20400, 1)
				job, run := computeJob(t, f, "batch")
				requests := []financial.ComputeRequest{claimComputeJob(t, f, job, run, 0), f.request(0), f.request(1)}
				requests[1].BillableMB = 136
				requests[2].BillableMB = 136
				var mu sync.Mutex
				var winners []financial.ComputeLease
				var wg sync.WaitGroup
				for _, r := range requests {
					wg.Go(func() {
						l, err := store.AcquireFinancialCompute(t.Context(), r)
						mu.Lock()
						defer mu.Unlock()
						if err == nil {
							winners = append(winners, l)
						} else if !errors.Is(err, financial.ErrComputeBudget) {
							t.Errorf("grant: %v", err)
						}
					})
				}
				wg.Wait()
				if len(winners) != 1 {
					t.Fatalf("shared funding winners=%d", len(winners))
				}
				b := f.balance(t)
				if b.AllowanceReserved != 20400 || b.UnconfirmedLeases != 1 || !b.Held {
					t.Fatalf("mixed funding: %+v", b)
				}
				if err := store.PrepareFinancialJobCompute(t.Context(), f.account.ID, job.ID); !errors.Is(err, financial.ErrComputeBudget) {
					t.Fatalf("held queued task admitted: %v", err)
				}
			})
			t.Run("job_gross_denial_does_not_consume_account_allowance", func(t *testing.T) {
				f := newAccountComputeFixture(t, store, 1000000, 1000)
				job, run := computeJob(t, f, "batch")
				spec := f.policy.Spec
				spec.Scope = financial.BudgetScope{Kind: "job", ID: job.ID}
				spec.Basis = "gross_usage"
				spec.LimitMillicents = 1
				p, err := store.CreateFinancialBudget(t.Context(), f.account.ID, uuid.NewString(), "test", spec)
				if err != nil {
					t.Fatal(err)
				}
				r := claimComputeJob(t, f, job, run, 0)
				l, err := store.AcquireFinancialCompute(t.Context(), r)
				if !errors.Is(err, financial.ErrComputeBudget) || !l.Denied || l.SettledAt == nil || l.QuantityReserved != 0 {
					t.Fatalf("denial: %+v %v", l, err)
				}
				b := f.balance(t)
				if b.AllowanceReserved != 0 || b.AllowanceCommitted != 0 || b.Held {
					t.Fatalf("job denial consumed account: %+v", b)
				}
				jb, err := store.FinancialComputeBalance(t.Context(), p, time.Now())
				if err != nil || !jb.Held || jb.JobID != job.ID || jb.AppID != "" || jb.ScopeKind != "job" || jb.CommittedMillicents != 0 || jb.ReservedMillicents != 0 {
					t.Fatalf("job hold: %+v %v", jb, err)
				}
				before, err := store.JobTaskGet(t.Context(), run.ID, 0)
				if err != nil {
					t.Fatal(err)
				}
				// A negative receipt proves the owner granted no execution; the
				// scheduler can now return the unchanged attempt to its queue.
				if err := store.DeferFinancialJobClaim(t.Context(), run.ID, 0, r.InstanceID, jobToken(r), time.Now().Add(time.Minute)); err != nil {
					t.Fatal(err)
				}
				after, err := store.JobTaskGet(t.Context(), run.ID, 0)
				if err != nil || after.Status != "queued" || after.Attempt != before.Attempt || after.InstanceID != nil || after.LeaseToken != nil {
					t.Fatalf("budget consumed attempt: %+v %v", after, err)
				}
				if _, err := store.AcquireFinancialCompute(t.Context(), r); !errors.Is(err, financial.ErrComputeFence) {
					t.Fatalf("late denied claim revived: %v", err)
				}
			})
			t.Run("cancel_before_grant_is_durable", func(t *testing.T) {
				f := newAccountComputeFixture(t, store, 1000000, 1000)
				job, run := computeJob(t, f, "batch")
				r := claimComputeJob(t, f, job, run, 0)
				l, err := store.CancelFinancialCompute(t.Context(), financial.ComputeRequest{NodeID: r.NodeID, Incarnation: r.Incarnation, InstanceID: r.InstanceID})
				if err != nil || l.JobID != job.ID || l.SettledAt == nil {
					t.Fatalf("cancel lost-response claim: %+v %v", l, err)
				}
				if _, err := store.AcquireFinancialCompute(t.Context(), r); !errors.Is(err, financial.ErrComputeFence) {
					t.Fatalf("late boot revived cancelled attempt: %v", err)
				}
				if b := f.balance(t); b.AllowanceReserved != 0 || b.AllowanceCommitted != 0 {
					t.Fatalf("unstarted claim spent funding: %+v", b)
				}
			})
			t.Run("restart_cannot_fund_a_prior_unstarted_claim", func(t *testing.T) {
				f := newAccountComputeFixture(t, store, 1000000, 1000)
				job, run := computeJob(t, f, "batch")
				r := claimComputeJob(t, f, job, run, 0)
				owner := f.owners[0]
				owner.Incarnation = uuid.NewString()
				if err := store.RegisterComputeNodeRuntimeIdentity(t.Context(), owner); err != nil {
					t.Fatal(err)
				}
				if err := store.RegisterFinancialComputeOwner(t.Context(), owner.NodeID, owner.Incarnation); err != nil {
					t.Fatal(err)
				}
				r.Incarnation = owner.Incarnation
				if _, err := store.AcquireFinancialCompute(t.Context(), r); !errors.Is(err, financial.ErrComputeFence) {
					t.Fatalf("new owner revived old unstarted claim: %v", err)
				}
				if b := f.balance(t); b.AllowanceReserved != 0 || b.ReservedMillicents != 0 {
					t.Fatalf("restart minted funding: %+v", b)
				}
				if _, err := store.CancelFinancialCompute(t.Context(), financial.ComputeRequest{NodeID: r.NodeID, Incarnation: r.Incarnation, InstanceID: r.InstanceID}); err != nil {
					t.Fatalf("replacement owner cannot close old claim: %v", err)
				}
			})
			t.Run("retry_retains_old_physical_funding", func(t *testing.T) {
				f := newAccountComputeFixture(t, store, 20400, 1)
				job, run := computeJob(t, f, "batch")
				r := claimComputeJob(t, f, job, run, 0)
				l, err := store.AcquireFinancialCompute(t.Context(), r)
				if err != nil {
					t.Fatal(err)
				}
				if _, err := store.JobTaskFailBoot(t.Context(), run.ID, 0, r.InstanceID, jobToken(r), 1, time.Now().Add(-time.Second), "lost boot reply"); err != nil {
					t.Fatal(err)
				}
				retry := claimComputeJob(t, f, job, run, 0)
				if retry.AttemptID == r.AttemptID || retry.InstanceID == r.InstanceID {
					t.Fatal("retry reused physical identity")
				}
				if _, err := store.AcquireFinancialCompute(t.Context(), retry); !errors.Is(err, financial.ErrComputeBudget) {
					t.Fatalf("retry spent unconfirmed funding: %v", err)
				}
				if b := f.balance(t); b.AllowanceReserved != 20400 || b.UnconfirmedLeases != 1 {
					t.Fatalf("retry reclaimed live funding: %+v", b)
				}
				if _, err := store.RenewFinancialCompute(t.Context(), jobComputeMutation(l)); !errors.Is(err, financial.ErrComputeFence) {
					t.Fatalf("superseded task renewed: %v", err)
				}
				if _, err := store.SettleFinancialCompute(t.Context(), jobComputeMutation(l)); err != nil {
					t.Fatal(err)
				}
				if b := f.balance(t); b.AllowanceReserved != 0 || b.AllowanceCommitted == 0 {
					t.Fatalf("retired retry settlement: %+v", b)
				}
				if _, err := store.AcquireFinancialCompute(t.Context(), r); !errors.Is(err, financial.ErrComputeFence) {
					t.Fatalf("old attempt escaped after settlement: %v", err)
				}
			})
		})
	}
}

func computeJob(t *testing.T, f accountComputeFixture, kind string) (state.Job, state.JobRun) {
	t.Helper()
	var j state.Job
	var err error
	if kind == "recurring" {
		j, err = f.store.(state.JobScheduleCreateStore).JobCreateScheduledIfUnderQuota(t.Context(), state.Job{AccountID: f.account.ID, Name: "job-" + uuid.NewString()[:8], Kind: kind, ImageRef: "example/image", Command: []string{"/job"}, RAMMB: 128, TaskTimeoutS: 60, MaxParallelism: 1, RetryMax: 1, CronSchedule: "* * * * *", CronTimezone: "UTC"}, 10)
	} else {
		j, err = f.store.JobCreate(t.Context(), f.account.ID, "job-"+uuid.NewString()[:8], kind, "example/image", []string{"/job"}, 128, 60, 1, 1, nil)
	}
	if err != nil {
		t.Fatal(err)
	}
	r, _, err := f.store.JobRunCreate(t.Context(), j.ID, f.account.ID, "manual", nil, nil, nil, nil, 1)
	if err != nil {
		t.Fatal(err)
	}
	return j, r
}

// The task lease token is opaque; the financial attempt uses its stable hash.
// Keeping a deterministic token here lets the fixture recover it without
// exposing production execution secrets through a public financial API.
func jobToken(r financial.ComputeRequest) string {
	return uuid.NewSHA1(uuid.NameSpaceOID, []byte("claim:"+r.InstanceID)).String()
}

func claimComputeJob(t *testing.T, f accountComputeFixture, j state.Job, run state.JobRun, index int) financial.ComputeRequest {
	t.Helper()
	if err := f.store.PrepareFinancialJobCompute(t.Context(), f.account.ID, j.ID); err != nil {
		t.Fatal(err)
	}
	o := f.owners[0]
	r := financial.ComputeRequest{AccountID: f.account.ID, JobID: j.ID, JobRunID: run.ID, JobTaskIndex: index, InstanceID: uuid.NewString(), NodeID: o.NodeID, Incarnation: o.Incarnation, Fenced: true, BillableMB: api.BillableRAMMBWithSidecars(128, nil)}
	token := jobToken(r)
	r.AttemptID = financial.ComputeJobAttemptID(run.ID, index, token)
	if _, err := f.store.CreateAndClaimJobInstance(t.Context(), r.InstanceID, j.ID, run.ID, index, string(state.StateColdBooting), 128, o.NodeID, r.InstanceID, token, time.Now().Add(time.Hour), o.NodeID); err != nil {
		t.Fatal(err)
	}
	return r
}

func jobComputeMutation(l financial.ComputeLease) financial.ComputeRequest {
	return financial.ComputeRequest{LeaseID: l.ID, Fence: l.Fence, RequestID: uuid.NewString(), Revision: l.Revision, AccountID: l.AccountID, AppID: l.AppID, JobID: l.JobID, JobRunID: l.JobRunID, JobTaskIndex: l.JobTaskIndex, InstanceID: l.InstanceID, NodeID: l.NodeID, Incarnation: l.Incarnation}
}
