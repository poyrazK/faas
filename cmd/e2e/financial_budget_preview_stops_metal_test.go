//go:build metal

package e2e_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/e2etest"
	"github.com/onebox-faas/faas/pkg/financial"
	"github.com/onebox-faas/faas/pkg/state"
)

// adr: 566 — required on native x86_64 KVM, followed by make leakcheck.
func TestFinancialBudgetPreviewStopMetal(t *testing.T) {
	if !metalAvailable(t) {
		return
	}
	pool := pgtest.OpenMigrated(t)
	if err := dbMigrateUp(t, pool); err != nil {
		t.Fatal(err)
	}
	registry := e2etest.NewFakeRegistry()
	t.Cleanup(func() { registry.Close() })
	base, _ := e2etest.HelloImage("onebox-faas/builder-base", "")
	e2etest.OverrideBuilderBase(t, registry.AddImage("onebox-faas/builder-base", base))
	deployBase, _ := e2etest.BaseLayerImage("onebox-faas/deploy-base", helloBody)
	registry.AddImage("onebox-faas/deploy-base", deployBase)
	e2etest.OverrideDeployBase(t, registry.Host()+"/onebox-faas/deploy-base:latest")
	h := e2etest.Start(t, pool, e2etest.DeployWake)
	ctx := t.Context()
	key := h.SeedAccount(ctx, api.PlanPro)
	account := accountIDFromKey(t, ctx, pool, key)
	s := state.NewPgStore(pool)
	image, _ := e2etest.HelloImageAboveBase("library/budget-hello", helloBody)
	ref := registry.AddImage("library/budget-hello", image)
	// apid inserts an app unplaced and announces it so schedd claims a node;
	// these rows bypass apid, so place them on the harness node directly or
	// the gateway refuses them ("empty NodeID").
	var nodeID string
	if err := pool.QueryRow(ctx, `SELECT id FROM compute_nodes WHERE name = 'default-local'`).Scan(&nodeID); err != nil {
		t.Fatal(err)
	}
	var preview state.App
	for _, slug := range []string{"budget-production", "budget-preview"} {
		parent := ""
		if slug == "budget-preview" {
			parent = "budget-production"
		}
		app, err := s.CreateApp(ctx, state.App{AccountID: account, Slug: slug, PreviewOfSlug: parent, NodeID: nodeID, Type: state.AppTypeApp, Runtime: "node22", RAMMB: 128, MaxConcurrency: 2, IdleTimeoutS: 300})
		if err != nil {
			t.Fatal(err)
		}
		if parent != "" {
			preview = app
		}
		body, status := doReq(t, h, key, http.MethodPost, "/v1/apps/"+slug+"/deployments", api.CreateDeploymentRequest{Image: ref})
		var dep api.DeploymentResponse
		if status != http.StatusAccepted || json.Unmarshal(body, &dep) != nil {
			t.Fatalf("deploy %s: %d %s", slug, status, body)
		}
		if _, err := e2etest.WaitForDeploymentLive(ctx, t, pool, dep.ID, 10*time.Minute); err != nil {
			h.DumpLogs(t)
			t.Fatal(err)
		}
	}
	request := func(slug string, want int) []byte {
		t.Helper()
		body, status := doGetWithHost(t, &http.Client{}, h.GatewayURL+"/", slug+".apps.test.example", 30*time.Second)
		if status != want {
			t.Fatalf("%s: status=%d want=%d body=%s", slug, status, want, body)
		}
		return body
	}
	request("budget-preview", http.StatusOK)
	request("budget-production", http.StatusOK)
	p, err := s.CreateFinancialBudget(ctx, account, uuid.NewString(), "native-acceptance", financial.BudgetSpec{Name: "Previews", Scope: financial.BudgetScope{Kind: "account"}, Currency: "USD", Meters: []string{"compute"}, Basis: "gross_usage", Mode: "monitored", Action: "stop_previews", ResumeRule: "manual", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	month, amount := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC), int64(0)
	if err := s.RecordFinancialBudgetEvaluation(ctx, account, p.ID, p.Revision, financial.BudgetEvaluation{PeriodStart: month, PeriodEnd: month.AddDate(0, 1, 0), AsOf: now, EvaluatedAt: now, KnownMillicents: &amount, Fresh: true}); err != nil {
		t.Fatal(err)
	}
	if body := request("budget-preview", http.StatusPaymentRequired); !strings.Contains(string(body), "financial_budget_held") {
		t.Fatalf("unstable budget problem: %s", body)
	}
	request("budget-production", http.StatusOK)
	deadline := time.Now().Add(time.Minute)
	for {
		stops, err := s.ActiveFinancialBudgetStops(ctx, account, time.Now().UTC())
		if err != nil || len(stops) != 1 {
			t.Fatalf("decision read: %+v %v", stops, err)
		}
		targets, err := s.FinancialBudgetStopTargets(ctx, account, stops[0].ID)
		if err != nil {
			t.Fatal(err)
		}
		if len(targets) == 1 && targets[0].AppID == preview.ID && targets[0].State == "enforced" && targets[0].ResidentCount == 0 {
			break
		}
		if time.Now().After(deadline) {
			h.DumpLogs(t)
			t.Fatalf("native stop not confirmed: %+v", targets)
		}
		select {
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-time.After(100 * time.Millisecond):
		}
	}
	request("budget-preview", http.StatusPaymentRequired) // A cold wake cannot escape.
	p.Spec.Enabled = false
	if _, err := s.UpdateFinancialBudget(ctx, account, p.ID, p.Revision, "native-acceptance", p.Spec); err != nil {
		t.Fatal(err)
	}
	request("budget-preview", http.StatusOK)
}
