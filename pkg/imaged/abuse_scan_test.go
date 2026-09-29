// adr: 368 — build-time abuse signature scan.
package imaged

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/abusescan"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func seedAbuseScanDeployment(t *testing.T) (*state.MemStore, state.App, state.Deployment) {
	t.Helper()
	ctx := context.Background()
	store := state.NewMemStore()
	acct, err := store.CreateAccount(ctx, "abuse-scan@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: "abuse-scan", Type: state.AppTypeApp, RAMMB: 512, MaxConcurrency: 1})
	if err != nil {
		t.Fatal(err)
	}
	dep, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage, ImageDigest: "sha256:abc"})
	if err != nil {
		t.Fatal(err)
	}
	return store, app, dep
}

func abuseScanEvents(t *testing.T, store *state.MemStore, depID string) []state.Event {
	t.Helper()
	events, err := store.ListEvents(context.Background(), depID, 100)
	if err != nil {
		t.Fatalf("ListEvents: %v", err)
	}
	var out []state.Event
	for _, e := range events {
		if e.Kind == "deployment.abuse_scan" {
			out = append(out, e)
		}
	}
	return out
}

// A blocking finding fails the deploy with image_abuse_detected, names the
// rule and file, and is audited.
func TestHandleAbuseFindingsBlocks(t *testing.T) {
	store, app, dep := seedAbuseScanDeployment(t)
	h := newHandler(store)
	err := h.handleAbuseFindings(context.Background(), app, dep, []abusescan.Finding{
		{Path: "usr/local/bin/worker", RuleID: "miner-xmrig", Category: abusescan.CategoryMiner, Action: abusescan.ActionBlock},
		{Path: "app/pools.txt", RuleID: "miner-pool-domain", Category: abusescan.CategoryMiner, Action: abusescan.ActionFlag},
	})
	if !errors.Is(err, errImageAbuseDetected) {
		t.Fatalf("err = %v, want errImageAbuseDetected", err)
	}
	got, _ := store.DeploymentByID(context.Background(), dep.ID)
	if got.Status != state.DeployFailed || got.ErrorCode != api.CodeImageAbuseDetected {
		t.Fatalf("deployment = %s/%s, want failed/%s", got.Status, got.ErrorCode, api.CodeImageAbuseDetected)
	}
	if !strings.Contains(got.Error, "miner-xmrig in usr/local/bin/worker") || strings.Contains(got.Error, "pools.txt") {
		t.Fatalf("error detail %q must name the blocking rule and file only", got.Error)
	}
	if ev := abuseScanEvents(t, store, dep.ID); len(ev) != 1 || !strings.Contains(string(ev[0].Data), `"blocking":true`) {
		t.Fatalf("audit events = %+v, want one blocking deployment.abuse_scan", ev)
	}
}

// Flag-only findings are audited but the deploy continues; no findings do
// nothing.
func TestHandleAbuseFindingsFlagOnly(t *testing.T) {
	store, app, dep := seedAbuseScanDeployment(t)
	h := newHandler(store)
	if err := h.handleAbuseFindings(context.Background(), app, dep, nil); err != nil {
		t.Fatalf("no findings: %v", err)
	}
	if err := h.handleAbuseFindings(context.Background(), app, dep, []abusescan.Finding{
		{Path: "bin/frps", RuleID: "proxy-server", Category: abusescan.CategoryProxy, Action: abusescan.ActionFlag},
	}); err != nil {
		t.Fatalf("flag-only findings failed the deploy: %v", err)
	}
	got, _ := store.DeploymentByID(context.Background(), dep.ID)
	if got.Status == state.DeployFailed {
		t.Fatal("a flag-only finding failed the deployment")
	}
	if ev := abuseScanEvents(t, store, dep.ID); len(ev) != 1 || !strings.Contains(string(ev[0].Data), `"blocking":false`) {
		t.Fatalf("audit events = %+v, want one flag-only deployment.abuse_scan", ev)
	}
}
