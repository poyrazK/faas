package apidserver_test

import (
	"encoding/json"
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

func TestFinancialBudgetAccountStrictEgress(t *testing.T) {
	e := apidserver.Setup(t, api.PlanHobby)
	store := *e.Store()
	account := *e.Acct()
	ctx := t.Context()
	nodes, err := store.ListComputeNodes(ctx, false)
	if err != nil {
		t.Fatal(err)
	}
	var identity runtimeadmission.Identity
	for _, n := range nodes {
		owner := runtimeadmission.Identity{NodeID: n.ID, Incarnation: uuid.NewString(), ProtocolVersion: 1}
		if err := store.RegisterComputeNodeRuntimeIdentity(ctx, owner); err != nil {
			t.Fatal(err)
		}
		identity = owner
		if err := store.RegisterFinancialComputeOwner(ctx, n.ID, owner.Incarnation); err != nil {
			t.Fatal(err)
		}
	}
	for at := account.CreatedAt.Truncate(time.Minute); !at.After(time.Now().UTC().Truncate(time.Minute)); at = at.Add(time.Minute) {
		if err := store.RecordFinancialSamplingWindow(ctx, at, true, true); err != nil {
			t.Fatal(err)
		}
	}
	month := financial.ComputeMonth(time.Now())
	_, err = store.PutFinancialPriceSnapshot(ctx, state.FinancialPriceSnapshot{AccountID: account.ID, PeriodStart: month, PeriodEnd: month.AddDate(0, 1, 0), EffectiveFrom: month, Plan: api.PlanHobby, DeliveryMode: "live", Price: financial.Price{Version: "account-api", Meter: "compute", Currency: "USD", Unit: "mb_seconds", UnitQuantity: api.SecondsPerGBHour, MillicentsPerUnit: api.OverageMillicentsPerGBHour, IncludedQuantity: 153600}})
	if err != nil {
		t.Fatal(err)
	}

	_, err = store.PutFinancialPriceSnapshot(ctx, state.FinancialPriceSnapshot{AccountID: account.ID, PeriodStart: month, PeriodEnd: month.AddDate(0, 1, 0), EffectiveFrom: month, Plan: api.PlanHobby, DeliveryMode: "live", Price: financial.Price{Version: "account-api-egress", Meter: "egress", Currency: "USD", Unit: "interface_bytes", UnitQuantity: 1024 * 1024, MillicentsPerUnit: 1, IncludedQuantity: api.FinancialEgressCreditBytes}})
	if err != nil {
		t.Fatal(err)
	}
	spec := financialDraftSpec()
	spec.Enabled = true
	spec.Mode = "strict"
	spec.Action = "suspend_workloads"
	spec.DrainSeconds = 0
	spec.NotifyMillicents = []int64{}
	spec.Meters = []string{"compute", "egress"}
	spec.LimitMillicents = 0
	rec := e.Do(t, "POST", "/v1/billing/budgets", api.CreateFinancialBudgetRequest{Spec: spec}, map[string]string{"Idempotency-Key": "account-strict-egress"})
	p := decodeFinancialBudget(t, rec, http.StatusCreated)
	if !p.EnforcementReady || p.Runtime == nil || p.Runtime.Compute == nil || p.Runtime.Compute.Egress == nil {
		t.Fatalf("combined activation: %s", rec.Body)
	}
	b := p.Runtime.Compute
	if b.Egress.AllowanceGranted != api.FinancialEgressCreditBytes || b.Egress.AllowanceRemaining != api.FinancialEgressCreditBytes || b.RemainingMillicents != 0 {
		t.Fatalf("allowances: %+v", b)
	}
	if !strings.Contains(rec.Body.String(), "strict_account_net_compute_egress") || !strings.Contains(rec.Body.String(), "unused_egress_credits_commit_conservatively") {
		t.Fatal("combined response overstates invoice equivalence")
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: account.ID, Slug: "egress-api-app", RAMMB: 128, MaxConcurrency: 2})
	if err != nil {
		t.Fatal(err)
	}
	owner := identity
	request := financial.ComputeRequest{AccountID: account.ID, AppID: app.ID, InstanceID: uuid.NewString(), AttemptID: uuid.NewString(), Fenced: true, NodeID: owner.NodeID, Incarnation: owner.Incarnation, BillableMB: 1024}
	l, err := store.AcquireFinancialCompute(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	request.InstanceID = uuid.NewString()
	request.AttemptID = uuid.NewString()
	if _, err := store.AcquireFinancialCompute(ctx, request); err == nil {
		t.Fatal("unfunded second attempt admitted")
	}
	got := decodeFinancialBudget(t, e.Do(t, "GET", "/v1/billing/budgets/"+p.ID, nil, nil), http.StatusOK)
	if !got.Runtime.Compute.Held || got.Runtime.Compute.Egress.ReservedBytes != l.EgressGranted || got.Runtime.Compute.Egress.AllowanceRemaining != 0 {
		t.Fatalf("hold or reservation missing: %+v", got.Runtime.Compute)
	}
	var wire map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &wire); err != nil {
		t.Fatal(err)
	}
}
