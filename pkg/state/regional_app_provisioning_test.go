package state

// adr: 797
import (
	"encoding/json"
	"errors"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

func provisionEntry() OrgActivity {
	return OrgActivity{Kind: "app.created", ActorType: OrgActivityActorUser, ActorLabel: "test", SourceType: "app.created"}
}

func TestRegionalAppProvisionMemoryContract(t *testing.T) {
	s := NewMemStore()
	a, err := s.CreateAccount(t.Context(), "provision@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	regionalProvisionContract(t, s, s, uuid.NewString(), a)
}

func regionalProvisionContract(t *testing.T, s Store, pstore RegionalAppProvisionStore, cell string, account Account) {
	t.Helper()
	ctx := t.Context()
	if _, err := s.UpsertComputeRegion(ctx, ComputeRegion{ID: "eu-fsn", DisplayName: "eu-fsn", AcceptingNewApps: true}); err != nil {
		t.Fatal(err)
	}
	request := api.CreateAppRequest{Slug: "regional-provision", ComputeRegion: "eu-fsn", RAMMB: 128}
	raw, _ := json.Marshal(request)
	p := RegionalAppProvision{ProvisionID: uuid.NewString(), GlobalAppID: uuid.NewString(), CellID: cell, Region: "eu-fsn", AccountID: account.ID, AppSlug: request.Slug, Request: raw}
	app := App{AccountID: account.ID, Slug: request.Slug, ComputeRegion: p.Region, RAMMB: 128, MaxConcurrency: 1, Type: AppTypeApp}
	limits := api.MustLimitsFor(api.PlanPro)
	var wg sync.WaitGroup
	var createdCount atomic.Int32
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			got, created, _, err := pstore.CreateRegionalAppProvision(ctx, p, app, limits, provisionEntry())
			if err != nil || got.ProvisionID != p.ProvisionID {
				t.Errorf("concurrent replay: %+v %v", got, err)
			}
			if created {
				createdCount.Add(1)
			}
		}()
	}
	wg.Wait()
	if createdCount.Load() != 1 {
		t.Fatalf("created %d apps", createdCount.Load())
	}
	first, err := pstore.RegionalAppProvisionReceipt(ctx, cell, account.ID, p.ProvisionID)
	if err != nil {
		t.Fatal(err)
	}
	if err := CheckRegionalRuntimeQuotaAdmission(ctx, s, first.AppID); !errors.Is(err, ErrRegionalRuntimeQuotaFrozen) {
		t.Fatalf("provisioned app borrowed regional admission: %v", err)
	}
	node, err := s.CreateComputeNode(ctx, ComputeNode{Name: "provision-test-node", TargetURL: "unix:///provision-test-node", Region: &p.Region, Active: true, AdmissionCeilingMB: 4096, MemMB: 8192, VPCPUs: 4, VCPUBudget: 8, MaxConcurrency: 32})
	if err != nil {
		t.Fatal(err)
	}
	dep, err := s.CreateDeployment(ctx, Deployment{AppID: first.AppID, Kind: DeploymentKindImage, ImageDigest: "sha256:provision-intent", Status: DeployLive})
	if err != nil {
		t.Fatal(err)
	}
	for _, resident := range []State{StateWaking, StateColdBooting, StateRunning, StateWarm} {
		if _, err := s.CreateInstance(ctx, first.AppID, dep.ID, string(resident), 128, node.ID, uuid.NewString()); !errors.Is(err, ErrRegionalRuntimeQuotaFrozen) {
			t.Fatalf("provisioned %s admission: %v", resident, err)
		}
	}
	if _, err := pstore.RegionalAppProvisionReceipt(ctx, cell, uuid.NewString(), p.ProvisionID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("foreign receipt: %v", err)
	}
	p.Request, _ = json.Marshal(api.CreateAppRequest{Slug: request.Slug, ComputeRegion: p.Region, RAMMB: 256})
	if _, _, _, err := pstore.CreateRegionalAppProvision(ctx, p, app, limits, provisionEntry()); !errors.Is(err, ErrConflict) {
		t.Fatalf("changed request: %v", err)
	}
	p.Request = raw
	p.ProvisionID = uuid.NewString()
	if _, _, _, err := pstore.CreateRegionalAppProvision(ctx, p, app, limits, provisionEntry()); !errors.Is(err, ErrConflict) {
		t.Fatalf("replacement provision: %v", err)
	}
	p.ProvisionID = first.ProvisionID
	quotaIntent := p
	quotaIntent.ProvisionID, quotaIntent.GlobalAppID, quotaIntent.AppSlug = uuid.NewString(), uuid.NewString(), "quota-shop"
	quotaIntent.Request, _ = json.Marshal(api.CreateAppRequest{Slug: quotaIntent.AppSlug, ComputeRegion: p.Region, RAMMB: 128})
	quotaApp := app
	quotaApp.Slug = quotaIntent.AppSlug
	oneSlot := limits
	oneSlot.DeployedApps = 1
	if _, _, _, err := pstore.CreateRegionalAppProvision(ctx, quotaIntent, quotaApp, oneSlot, provisionEntry()); err == nil {
		t.Fatal("regional quota bypassed")
	}
	if _, err := pstore.RegionalAppProvisionReceipt(ctx, cell, account.ID, quotaIntent.ProvisionID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("failed quota retained receipt: %v", err)
	}
	if _, err := s.ScheduleAppDeletion(ctx, first.AppID, first.CreatedAt); err != nil {
		t.Fatal(err)
	}
	again, created, _, err := pstore.CreateRegionalAppProvision(ctx, p, app, limits, provisionEntry())
	if err != nil || created || again.AppID != first.AppID {
		t.Fatalf("removed app replay: %+v created=%t %v", again, created, err)
	}
}
