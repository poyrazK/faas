package apidserver_test

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/statetest"
)

// listReadCountingStore counts the per-row and batch reads the app and
// deployment lists make.
type listReadCountingStore struct {
	MemStore *state.MemStore
	mu       sync.Mutex
	counts   map[string]int
}

func (s *listReadCountingStore) count(name string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.counts == nil {
		s.counts = map[string]int{}
	}
	s.counts[name]++
}

func (s *listReadCountingStore) reads() map[string]int {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := map[string]int{}
	for k, v := range s.counts {
		out[k] = v
	}
	return out
}

func (s *listReadCountingStore) DefaultCustomDomain(ctx context.Context, appID string) (string, error) {
	s.count("DefaultCustomDomain")
	return s.MemStore.DefaultCustomDomain(ctx, appID)
}

func (s *listReadCountingStore) DefaultCustomDomains(ctx context.Context, appIDs []string) (map[string]string, error) {
	s.count("DefaultCustomDomains")
	return s.MemStore.DefaultCustomDomains(ctx, appIDs)
}

func (s *listReadCountingStore) DeploymentWorkerDrainStatus(ctx context.Context, id string) (*state.DeploymentWorkerDrainStatus, error) {
	s.count("DeploymentWorkerDrainStatus")
	return s.MemStore.DeploymentWorkerDrainStatus(ctx, id)
}

func (s *listReadCountingStore) DeploymentWorkerDrainStatuses(ctx context.Context, ids []string) (map[string]*state.DeploymentWorkerDrainStatus, error) {
	s.count("DeploymentWorkerDrainStatuses")
	return s.MemStore.DeploymentWorkerDrainStatuses(ctx, ids)
}

func (s *listReadCountingStore) MCPTaskPromotionHold(ctx context.Context, id string) (*state.MCPTaskPromotionHold, error) {
	s.count("MCPTaskPromotionHold")
	return s.MemStore.MCPTaskPromotionHold(ctx, id)
}

func (s *listReadCountingStore) MCPTaskPromotionHolds(ctx context.Context, ids []string) (map[string]*state.MCPTaskPromotionHold, error) {
	s.count("MCPTaskPromotionHolds")
	return s.MemStore.MCPTaskPromotionHolds(ctx, ids)
}

func TestAppListReadsDefaultDomainsInOneQuery(t *testing.T) {
	e := apidserver.Setup(t, api.PlanPro)
	ctx := t.Context()
	store := *e.Store()
	var withDomain state.App
	for i, slug := range []string{"batch-a", "batch-b", "batch-c"} {
		app, err := store.CreateApp(ctx, state.App{AccountID: (*e.Acct()).ID, Slug: slug, Type: state.AppTypeApp})
		if err != nil {
			t.Fatal(err)
		}
		if i == 1 {
			withDomain = app
		}
	}
	if _, err := store.CreateCustomDomain(ctx, "api.batch.example", withDomain.ID, "token"); err != nil {
		t.Fatal(err)
	}
	if err := store.MarkDomainVerified(ctx, "api.batch.example"); err != nil {
		t.Fatal(err)
	}
	if err := store.SetDefaultCustomDomain(ctx, withDomain.ID, "api.batch.example"); err != nil {
		t.Fatal(err)
	}
	counter := &listReadCountingStore{MemStore: store}
	(*(*e.S()).Store()) = statetest.NewMem(counter)

	r := e.Do(t, http.MethodGet, "/v1/apps", nil, nil)
	if r.Code != http.StatusOK {
		t.Fatalf("list apps = %d %s", r.Code, r.Body)
	}
	var apps []api.AppResponse
	if err := json.Unmarshal(r.Body.Bytes(), &apps); err != nil {
		t.Fatal(err)
	}
	if len(apps) != 3 {
		t.Fatalf("apps = %d, want 3", len(apps))
	}
	for _, a := range apps {
		want := ""
		if a.Slug == withDomain.Slug {
			want = "api.batch.example"
		}
		if a.DefaultDomain != want {
			t.Fatalf("%s default_domain = %q, want %q", a.Slug, a.DefaultDomain, want)
		}
	}
	if reads := counter.reads(); reads["DefaultCustomDomains"] != 1 || reads["DefaultCustomDomain"] != 0 {
		t.Fatalf("reads = %v, want one batch domain read and no per-app reads", reads)
	}
}

func TestDeploymentListReadsDecorationsInOneQueryEach(t *testing.T) {
	e := apidserver.Setup(t, api.PlanPro)
	ctx := t.Context()
	store := *e.Store()
	app, err := store.CreateApp(ctx, state.App{AccountID: (*e.Acct()).ID, Slug: "batch-deploys", Type: state.AppTypeApp})
	if err != nil {
		t.Fatal(err)
	}
	var drained string
	for i := range 6 {
		d, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage,
			ImageDigest: "sha256:" + strings.Repeat("1", 64), Scope: "default", Status: state.DeployPending})
		if err != nil {
			t.Fatal(err)
		}
		if i%2 == 0 {
			if err := store.UpdateDeploymentStatus(ctx, d.ID, state.DeploySuperseded, ""); err != nil {
				t.Fatal(err)
			}
			drained = d.ID
		}
	}
	data, _ := json.Marshal(map[string]any{"instance_id": uuid.NewString(), "outcome": "completed", "deadline": time.Now().UTC()})
	if _, err := store.AppendDeploymentAudit(ctx, state.DeploymentAudit{DeploymentID: uuid.MustParse(drained),
		Kind: state.DeployWorkerDrainCompleted, Actor: "system", At: time.Now().UTC(), Data: data}); err != nil {
		t.Fatal(err)
	}
	counter := &listReadCountingStore{MemStore: store}
	(*(*e.S()).Store()) = statetest.NewMem(counter)

	r := e.Do(t, http.MethodGet, "/v1/apps/"+app.Slug+"/deployments", nil, nil)
	if r.Code != http.StatusOK {
		t.Fatalf("list deployments = %d %s", r.Code, r.Body)
	}
	var list api.DeploymentListResponse
	if err := json.Unmarshal(r.Body.Bytes(), &list); err != nil {
		t.Fatal(err)
	}
	if len(list.Items) != 6 {
		t.Fatalf("deployments = %d, want 6", len(list.Items))
	}
	for _, item := range list.Items {
		if got := item.WorkerDrain != nil; got != (item.ID == drained) {
			t.Fatalf("deployment %s worker_drain = %+v", item.ID, item.WorkerDrain)
		}
	}
	reads := counter.reads()
	if reads["DeploymentWorkerDrainStatuses"] != 1 || reads["MCPTaskPromotionHolds"] != 1 ||
		reads["DeploymentWorkerDrainStatus"] != 0 || reads["MCPTaskPromotionHold"] != 0 {
		t.Fatalf("reads = %v, want one batch read of each kind and no per-row reads", reads)
	}
}
