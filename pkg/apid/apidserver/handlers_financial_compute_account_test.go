package apidserver_test

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/financial"
	"github.com/onebox-faas/faas/pkg/runtimeadmission"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestFinancialBudgetAccountStrictCompute(t *testing.T) {
	e := apidserver.Setup(t, api.PlanHobby)
	store := *e.Store()
	account := *e.Acct()
	ctx := t.Context()
	nodes, err := store.ListComputeNodes(ctx, false)
	if err != nil {
		t.Fatal(err)
	}
	for _, n := range nodes {
		owner := runtimeadmission.Identity{NodeID: n.ID, Incarnation: uuid.NewString(), ProtocolVersion: 1}
		if err := store.RegisterComputeNodeRuntimeIdentity(ctx, owner); err != nil {
			t.Fatal(err)
		}
		if err := store.RegisterFinancialComputeOwner(ctx, n.ID, owner.Incarnation); err != nil {
			t.Fatal(err)
		}
	}
	for at := account.CreatedAt.Truncate(time.Minute); at.Before(time.Now().UTC().Truncate(time.Minute)); at = at.Add(time.Minute) {
		if err := store.RecordFinancialSamplingWindow(ctx, at, true, false); err != nil {
			t.Fatal(err)
		}
	}
	month := financial.ComputeMonth(time.Now())
	_, err = store.PutFinancialPriceSnapshot(ctx, state.FinancialPriceSnapshot{AccountID: account.ID, PeriodStart: month, PeriodEnd: month.AddDate(0, 1, 0), EffectiveFrom: month, Plan: api.PlanHobby, DeliveryMode: "live", Price: financial.Price{Version: "account-api", Meter: "compute", Currency: "EUR", Unit: "mb_seconds", UnitQuantity: api.SecondsPerGBHour, MillicentsPerUnit: api.OverageMillicentsPerGBHour, IncludedQuantity: 153600}})
	if err != nil {
		t.Fatal(err)
	}
	spec := financialDraftSpec()
	spec.Enabled = true
	spec.Mode = "strict"
	spec.Action = "suspend_workloads"
	spec.DrainSeconds = 0
	spec.NotifyMillicents = []int64{}
	rec := e.Do(t, "POST", "/v1/billing/budgets", api.CreateFinancialBudgetRequest{Spec: spec}, map[string]string{"Idempotency-Key": "account-strict"})
	p := decodeFinancialBudget(t, rec, http.StatusCreated)
	if !p.EnforcementReady || p.NotificationReady || p.Runtime == nil || p.Runtime.Compute == nil {
		t.Fatalf("strict account response: %+v", p)
	}
	b := p.Runtime.Compute
	if b.ScopeKind != "account" || b.AccountID != account.ID || b.Basis != "net_usage" || b.AppID != "" || b.AllowanceGranted != 153600 || b.AllowanceRemaining != 153600 || strings.Contains(rec.Body.String(), `"app_id"`) {
		t.Fatalf("account allowance/identity: %s", rec.Body)
	}
	read := decodeFinancialBudget(t, e.Do(t, "GET", "/v1/billing/budgets/"+p.ID, nil, nil), http.StatusOK)
	if read.Runtime.Compute.AllowanceRemaining != 153600 {
		t.Fatal("read lost allowance")
	}
	// The API reports the unsupported producer constraint as an actionable
	// validation problem, rather than a generic capacity failure.
	job := api.CreateJobRequest{Name: "blocked-job", Kind: "batch", ImageRef: "alpine:latest", RAMMB: 128, TaskTimeoutSec: 30, MaxParallelism: 1}
	blocked := e.Do(t, "POST", "/v1/jobs", job, map[string]string{"Idempotency-Key": "unsupported-account-job"})
	if blocked.Code != http.StatusUnprocessableEntity || !strings.Contains(blocked.Body.String(), "financial_compute_job_unsupported") {
		t.Fatalf("job escaped account guard: %d %s", blocked.Code, blocked.Body)
	}
}
