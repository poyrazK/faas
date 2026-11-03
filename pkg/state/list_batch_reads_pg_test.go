//go:build !no_pg

package state_test

import (
	"encoding/json"
	"reflect"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// The list batch reads answer exactly what their per-row reads answer.
func TestPgListBatchReadsMatchPerRowReads(t *testing.T) {
	store, ctx := pgStore(t)
	suffix := uuid.NewString()[:8]
	account, err := store.CreateAccount(ctx, "list-batch-"+suffix+"@example.com", api.PlanPro)
	if err != nil {
		t.Fatalf("create account: %v", err)
	}
	var appIDs, deploymentIDs []string
	for _, name := range []string{"a", "b"} {
		app, err := store.CreateApp(ctx, state.App{AccountID: account.ID, Slug: "list-batch-" + name + "-" + suffix,
			Type: state.AppTypeApp, Status: state.AppActive, RAMMB: 256, MaxConcurrency: 1})
		if err != nil {
			t.Fatalf("create app: %v", err)
		}
		appIDs = append(appIDs, app.ID)
		for _, status := range []state.DeploymentStatus{state.DeployLive, state.DeployPending} {
			d, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage,
				ImageDigest: "sha256:list-batch-" + suffix, Status: status})
			if err != nil {
				t.Fatalf("create deployment: %v", err)
			}
			deploymentIDs = append(deploymentIDs, d.ID)
		}
	}
	domain := "list-batch-" + suffix + ".example.com"
	if _, err := store.CreateCustomDomain(ctx, domain, appIDs[0], "token"); err != nil {
		t.Fatalf("create domain: %v", err)
	}
	if err := store.MarkDomainVerified(ctx, domain); err != nil {
		t.Fatalf("verify domain: %v", err)
	}
	if err := store.SetDefaultCustomDomain(ctx, appIDs[0], domain); err != nil {
		t.Fatalf("default domain: %v", err)
	}
	for i, outcome := range []string{"draining", "completed"} {
		data, _ := json.Marshal(map[string]any{"instance_id": uuid.NewString(), "outcome": outcome, "deadline": time.Now().UTC().Add(time.Hour)})
		if _, err := store.AppendDeploymentAudit(ctx, state.DeploymentAudit{DeploymentID: uuid.MustParse(deploymentIDs[i*2]),
			Kind: state.DeployWorkerDrainStarted, Actor: "system", At: time.Now().UTC(), Data: data}); err != nil {
			t.Fatalf("append drain audit: %v", err)
		}
	}

	domains, err := store.DefaultCustomDomains(ctx, appIDs)
	if err != nil {
		t.Fatalf("DefaultCustomDomains: %v", err)
	}
	if !reflect.DeepEqual(domains, map[string]string{appIDs[0]: domain}) {
		t.Fatalf("domains = %v", domains)
	}
	drains, err := store.DeploymentWorkerDrainStatuses(ctx, deploymentIDs)
	if err != nil {
		t.Fatalf("DeploymentWorkerDrainStatuses: %v", err)
	}
	holds, err := store.MCPTaskPromotionHolds(ctx, deploymentIDs)
	if err != nil {
		t.Fatalf("MCPTaskPromotionHolds: %v", err)
	}
	for _, id := range deploymentIDs {
		drain, err := store.DeploymentWorkerDrainStatus(ctx, id)
		if err != nil {
			t.Fatalf("DeploymentWorkerDrainStatus: %v", err)
		}
		if !reflect.DeepEqual(drains[id], drain) {
			t.Fatalf("drain for %s: batch %+v, per row %+v", id, drains[id], drain)
		}
		hold, err := store.MCPTaskPromotionHold(ctx, id)
		if err != nil {
			t.Fatalf("MCPTaskPromotionHold: %v", err)
		}
		if !reflect.DeepEqual(holds[id], hold) {
			t.Fatalf("hold for %s: batch %+v, per row %+v", id, holds[id], hold)
		}
	}
	if len(drains) != 2 {
		t.Fatalf("drains = %v, want two deployments", drains)
	}
}
