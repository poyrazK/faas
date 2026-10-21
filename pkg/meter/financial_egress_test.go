package meter_test

import (
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/billing"
	"github.com/onebox-faas/faas/pkg/financial"
	"github.com/onebox-faas/faas/pkg/meter"
	"github.com/onebox-faas/faas/pkg/runtimeadmission"
	"github.com/onebox-faas/faas/pkg/state"
)

type fundedEgressProvider struct {
	recordingEgressProvider
	policy billing.MeterUsagePolicy
}

func (p *fundedEgressProvider) MeterUsagePolicy(plan api.Plan, m state.BillingMeter) (billing.MeterUsagePolicy, bool) {
	return p.policy, m == state.BillingMeterEgress && plan != api.PlanFree
}

func TestPushPendingFundedEgressDelivery(t *testing.T) {
	for _, scenario := range []string{"compatible", "above_rate", "lower_allowance", "excess_counter", "deleted_policy", "disabled_usage"} {
		t.Run(scenario, func(t *testing.T) {
			ctx := t.Context()
			store := state.NewMemStore()
			acct := makeBillableAccount(t, ctx, store, api.PlanHobby)
			app, err := store.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: "funded-egress-" + uuid.NewString()[:8], RAMMB: 128, MaxConcurrency: 2})
			if err != nil {
				t.Fatal(err)
			}
			nodes, err := store.ListComputeNodes(ctx, false)
			if err != nil || len(nodes) == 0 {
				t.Fatalf("nodes: %v", err)
			}
			var owner runtimeadmission.Identity
			for _, n := range nodes {
				owner = runtimeadmission.Identity{NodeID: n.ID, Incarnation: uuid.NewString(), ProtocolVersion: 1}
				if err := store.RegisterComputeNodeRuntimeIdentity(ctx, owner); err != nil {
					t.Fatal(err)
				}
				if err := store.RegisterFinancialComputeOwner(ctx, n.ID, owner.Incarnation); err != nil {
					t.Fatal(err)
				}
			}
			now := time.Now().UTC()
			month := financial.ComputeMonth(now)
			grant := int64(0)
			if scenario == "lower_allowance" {
				grant = api.FinancialEgressCreditBytes
			}
			for _, price := range []financial.Price{
				{Version: "funded-compute", Meter: "compute", Currency: "USD", Unit: "mb_seconds", UnitQuantity: api.SecondsPerGBHour, MillicentsPerUnit: api.OverageMillicentsPerGBHour, IncludedQuantity: 1000000},
				{Version: "funded-egress", Meter: "egress", Currency: "USD", Unit: "interface_bytes", UnitQuantity: 1 << 30, MillicentsPerUnit: 2000, IncludedQuantity: grant},
			} {
				_, err := store.PutFinancialPriceSnapshot(ctx, state.FinancialPriceSnapshot{AccountID: acct.ID, PeriodStart: month, PeriodEnd: month.AddDate(0, 1, 0), EffectiveFrom: month, Plan: api.PlanHobby, DeliveryMode: "live", Price: price})
				if err != nil {
					t.Fatal(err)
				}
			}
			for at := acct.CreatedAt.Truncate(time.Minute); !at.After(now.Truncate(time.Minute)); at = at.Add(time.Minute) {
				if err := store.RecordFinancialSamplingWindow(ctx, at, true, true); err != nil {
					t.Fatal(err)
				}
			}
			budget, err := store.CreateFinancialBudget(ctx, acct.ID, uuid.NewString(), "test", financial.BudgetSpec{Name: "funded delivery", Scope: financial.BudgetScope{Kind: "account"}, Currency: "USD", Meters: []string{"compute", "egress"}, Basis: "net_usage", LimitMillicents: 1000, Mode: "strict", Action: "suspend_workloads", ResumeRule: "manual", Enabled: true})
			if err != nil {
				t.Fatal(err)
			}
			l, err := store.AcquireFinancialCompute(ctx, financial.ComputeRequest{AccountID: acct.ID, AppID: app.ID, InstanceID: uuid.NewString(), AttemptID: uuid.NewString(), Fenced: true, NodeID: owner.NodeID, Incarnation: owner.Incarnation, BillableMB: 136})
			if err != nil {
				t.Fatal(err)
			}
			provider := &fundedEgressProvider{recordingEgressProvider: recordingEgressProvider{mode: billing.MeterDeliveryLive}, policy: billing.MeterUsagePolicy{Mode: billing.MeterDeliveryLive, EffectiveFrom: month, UnitQuantity: 1 << 30, MillicentsPerUnit: 2000}}
			if scenario == "above_rate" || scenario == "deleted_policy" {
				provider.policy.MillicentsPerUnit = 4000
			}
			if scenario == "deleted_policy" || scenario == "disabled_usage" {
				if _, err := store.DeleteFinancialBudget(ctx, acct.ID, budget.ID, budget.Revision, "test"); err != nil {
					t.Fatal(err)
				}
			}
			traffic := l.EgressGranted / 2
			if scenario == "excess_counter" || scenario == "disabled_usage" {
				traffic = 2*l.EgressGranted + 1
			}
			hour := now.Truncate(time.Hour)
			if err := store.AppendUsage(ctx, acct.ID, app.ID, l.InstanceID, hour, 0, 0, 0, 0, traffic, 0, 0, 0); err != nil {
				t.Fatal(err)
			}
			billedAt := hour.Add(2 * time.Hour)
			completeUsageThrough(t, ctx, store, billedAt)
			pusher := meter.NewPusher(store, provider, discardLog(), func() time.Time { return billedAt }, nil)
			_, err = pusher.PushPending(ctx, 30*24*time.Hour)
			calls := provider.MeterCalls()
			if scenario == "compatible" || scenario == "disabled_usage" {
				if err != nil || len(calls) != 1 || calls[0].Quantity != traffic {
					t.Fatalf("compatible delivery: calls=%+v err=%v", calls, err)
				}
			} else {
				if !errors.Is(err, financial.ErrComputeUnavailable) || len(calls) != 0 {
					t.Fatalf("unfunded provider event: calls=%+v err=%v", calls, err)
				}
				windows, readErr := store.PendingBillingMeterUsageWindows(ctx, "stripe", state.BillingMeterEgress, hour, hour.Add(time.Hour))
				if readErr != nil || len(windows) != 1 {
					t.Fatalf("held delivery got a receipt: %+v %v", windows, readErr)
				}
			}
		})
	}
}
