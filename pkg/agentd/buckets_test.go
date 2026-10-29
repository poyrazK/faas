// adr: 20261027090010, 20261027090014
package agentd_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/onebox-faas/faas/pkg/agentd"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/s3gateway"
	"github.com/onebox-faas/faas/pkg/state"
	agentstore "github.com/onebox-faas/faas/pkg/state/agents"
)

const (
	bucketID   = "b0c4e700-0000-4000-8000-000000000001"
	appID      = "a99a0000-0000-4000-8000-000000000001"
	accountRun = account
)

// memStorage is an in-memory artifact store.
type memStorage struct {
	mu      sync.Mutex
	objects map[string][]byte
	failPut error
}

func (m *memStorage) Put(_ context.Context, key string, r io.Reader) error {
	if m.failPut != nil {
		return m.failPut
	}
	b, err := io.ReadAll(r)
	if err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.objects == nil {
		m.objects = map[string][]byte{}
	}
	m.objects[key] = b
	return nil
}

func (m *memStorage) Get(_ context.Context, key string) (io.ReadCloser, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	b, ok := m.objects[key]
	if !ok {
		return nil, errors.New("not found")
	}
	return io.NopCloser(bytes.NewReader(b)), nil
}

func (m *memStorage) Delete(_ context.Context, key string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.objects, key)
	return nil
}

// fakeGateway is the account's bucket and the S3 gateway's grant endpoint:
// it accepts a PUT only for a grant the delivery made.
type fakeGateway struct {
	mu      sync.Mutex
	grants  map[string]state.ObjectUploadGrant
	objects map[string][]byte
	status  int
	missing bool
}

func (g *fakeGateway) GetObjectBucket(_ context.Context, account, app, id string) (state.ObjectBucket, error) {
	if g.missing || account != accountRun || app != appID || id != bucketID {
		return state.ObjectBucket{}, state.ErrNotFound
	}
	return state.ObjectBucket{ID: bucketID, AccountID: account, AppID: app, Name: "reports", State: "ready",
		BackendID: "local", BackendFingerprint: "f", PhysicalName: "p"}, nil
}

func (g *fakeGateway) CreateObjectUploadGrant(_ context.Context, grant state.ObjectUploadGrant, ttl int) (state.ObjectUploadGrant, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if ttl < 1 || ttl > api.MaxObjectSignedURLExpiresSeconds || grant.Bucket.ID != bucketID {
		return state.ObjectUploadGrant{}, state.ErrInvalidArgument
	}
	g.grants[grant.TokenHash] = grant
	return grant, nil
}

func (g *fakeGateway) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	g.mu.Lock()
	defer g.mu.Unlock()
	grant, ok := g.grants[s3gateway.UploadGrantTokenHash(r.URL.Query().Get(s3gateway.UploadGrantQueryParameter))]
	body, _ := io.ReadAll(r.Body)
	if !ok || r.Method != http.MethodPut || r.URL.Path != "/reports/"+grant.Key || int64(len(body)) != grant.SizeBytes ||
		r.Header.Get("X-Amz-Meta-Gregale-Agent-Run") == "" || r.Header.Get("Content-Type") != grant.Headers["Content-Type"] {
		w.WriteHeader(http.StatusForbidden)
		return
	}
	if g.status != 0 {
		w.WriteHeader(g.status)
		return
	}
	g.objects[grant.Key] = body
}

// bucketFixture has an agent that writes its files to the account's
// "reports" bucket, a replica that keeps output files, and the gateway.
func bucketFixture(t *testing.T, gw *fakeGateway) (*agentstore.Store, *agentd.Agentd, *agentd.Deliverer, *memStorage, string) {
	t.Helper()
	pool, store := openStore(t)
	ctx := context.Background()
	if _, err := pool.Exec(ctx, `INSERT INTO object_buckets (id, account_id, app_id) VALUES ($1, $2, $3)`, bucketID, account, appID); err != nil {
		t.Fatal(err)
	}
	spec := agentstore.VersionSpec{Harness: agentstore.Harness{Image: "ghcr.io/acme/agent:1", Run: []string{"ok", "{prompt}"}},
		PromptTemplate: "Report", RAMMB: 1024, TimeoutSec: 60,
		Deliveries: []agentstore.Delivery{{Type: api.AgentDeliveryBucket, Bucket: bucketID, Prefix: "reports/"}}}
	if _, _, err := store.CreateAgent(ctx, account, "reporter", "", spec, 10); err != nil {
		t.Fatal(err)
	}
	run, _, err := store.AdmitRun(ctx, agentstore.Admission{AccountID: account, Agent: "reporter", Trigger: "api",
		Limits: agentstore.AdmitLimits{RunsPerDay: 100, QueuedMax: 100, TimeoutMaxSec: 3600, RAMMBMax: 4096}})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewTLSServer(gw)
	t.Cleanup(server.Close)
	files := &memStorage{}
	deliverer := agentd.NewDeliverer(store, nil, agentd.DeliveryConfig{LeaseTTL: 3 * time.Second}, nil).
		WithBuckets(&agentd.Buckets{Grants: gw, Files: files, Endpoint: server.URL, HTTP: server.Client()})
	replica := newAgentd(t, store, &fakeSchedd{}).WithOutputFiles(&agentd.OutputFiles{Storage: files}).WithDeliveries(deliverer)
	return store, replica, deliverer, files, run.ID
}

func TestRunWritesItsFilesToABucket(t *testing.T) {
	gw := &fakeGateway{grants: map[string]state.ObjectUploadGrant{}, objects: map[string][]byte{}}
	store, replica, deliverer, files, id := bucketFixture(t, gw)
	reg := prometheus.NewRegistry()
	metrics, err := agentd.NewMetrics(reg)
	if err != nil {
		t.Fatal(err)
	}
	deliverer.WithMetrics(metrics)
	if claimed, err := replica.ClaimOnce(context.Background()); err != nil || !claimed {
		t.Fatalf("claim %v %v", claimed, err)
	}
	out, err := store.RunOutputs(context.Background(), account, id)
	if err != nil || len(out.Files) != 1 || out.Files[0].Key == "" || string(files.objects[out.Files[0].Key]) != "# ok" {
		t.Fatalf("kept files %+v %v %v", out.Files, files.objects, err)
	}
	if n, err := deliverer.DeliverOnce(context.Background()); err != nil || n != 1 {
		t.Fatalf("deliver %d %v", n, err)
	}
	key := "reports/" + id + "/report.md"
	if string(gw.objects[key]) != "# ok" {
		t.Fatalf("bucket objects %v", gw.objects)
	}
	run := runState(t, store, id)
	out, _ = store.RunOutputs(context.Background(), account, id)
	if run.State != agentstore.StateSucceeded || out.Deliveries[0].URL != "s3://reports/reports/"+id+"/" {
		t.Fatalf("run %+v deliveries %+v", run, out.Deliveries)
	}
	if got := counterValue(t, reg, "agent_deliveries_total", map[string]string{"destination": "bucket", "outcome": "delivered"}); got != 1 {
		t.Fatalf("agent_deliveries_total = %v", got)
	}
}

func TestBucketDeliveryOutcomes(t *testing.T) {
	for name, tc := range map[string]struct {
		status   int
		missing  bool
		delivery string
		run      string
	}{
		"refused":     {http.StatusForbidden, false, api.AgentDeliveryFailed, agentstore.StateFailed},
		"unavailable": {http.StatusServiceUnavailable, false, api.AgentDeliveryPending, agentstore.StateDelivering},
		"lost":        {http.StatusBadGateway, false, api.AgentDeliveryReconciling, agentstore.StateDelivering},
		"no bucket":   {0, true, api.AgentDeliveryFailed, agentstore.StateFailed},
	} {
		t.Run(name, func(t *testing.T) {
			gw := &fakeGateway{status: tc.status, missing: tc.missing, grants: map[string]state.ObjectUploadGrant{}, objects: map[string][]byte{}}
			store, replica, deliverer, _, id := bucketFixture(t, gw)
			if claimed, err := replica.ClaimOnce(context.Background()); err != nil || !claimed {
				t.Fatalf("claim %v %v", claimed, err)
			}
			if _, err := deliverer.DeliverOnce(context.Background()); err != nil {
				t.Fatal(err)
			}
			out, _ := store.RunOutputs(context.Background(), account, id)
			if out.Deliveries[0].State != tc.delivery || runState(t, store, id).State != tc.run {
				t.Fatalf("deliveries %+v run %s", out.Deliveries, runState(t, store, id).State)
			}
		})
	}
}

// A replica that cannot keep a succeeded run's files fails the run rather
// than finishing it with files no one can read.
func TestRunFailsWhenItsFilesCannotBeKept(t *testing.T) {
	gw := &fakeGateway{grants: map[string]state.ObjectUploadGrant{}, objects: map[string][]byte{}}
	store, replica, _, files, id := bucketFixture(t, gw)
	files.failPut = errors.New("disk full")
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if claimed, err := replica.ClaimOnce(ctx); err != nil || !claimed {
		t.Fatalf("claim %v %v", claimed, err)
	}
	if run := runState(t, store, id); run.State != agentstore.StateFailed || run.OutcomeCode != "outputs_unavailable" ||
		!strings.Contains(run.OutcomeMessage, "report.md") {
		t.Fatalf("run %+v", run)
	}
}

// counterValue reads one counter of a family from reg.
func counterValue(t *testing.T, reg *prometheus.Registry, name string, labels map[string]string) float64 {
	t.Helper()
	families, err := reg.Gather()
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range families {
		if f.GetName() != name {
			continue
		}
	metrics:
		for _, m := range f.GetMetric() {
			for _, l := range m.GetLabel() {
				if v, ok := labels[l.GetName()]; ok && v != l.GetValue() {
					continue metrics
				}
			}
			return m.GetCounter().GetValue()
		}
	}
	t.Fatalf("no %s %v", name, labels)
	return 0
}
