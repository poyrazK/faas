package receivers

import (
	"context"
	"errors"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	logspb "go.opentelemetry.io/proto/otlp/logs/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	apidpb "github.com/onebox-faas/faas/api/proto/onebox/faas/apid/v1"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/ratelimit/peraccount"
	"github.com/onebox-faas/faas/pkg/state"
)

// adr: 958 — on the shared mTLS listener a node exports guest spans and logs
// only for instances it runs (or ran before a migration), of the app,
// deployment and account the frame names; a local socket and an apid without
// a node resolver accept them as before.

type guestBindingFakeStore struct {
	mu        sync.Mutex
	apps      map[string]state.App
	instances map[string]state.Instance
	err       error
	reads     int
}

func (f *guestBindingFakeStore) AppByID(_ context.Context, id string) (state.App, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.reads++
	if f.err != nil {
		return state.App{}, f.err
	}
	app, ok := f.apps[id]
	if !ok {
		return state.App{}, state.ErrNotFound
	}
	return app, nil
}

func (f *guestBindingFakeStore) InstanceByID(_ context.Context, id string) (state.Instance, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.reads++
	if f.err != nil {
		return state.Instance{}, f.err
	}
	inst, ok := f.instances[id]
	if !ok {
		return state.Instance{}, state.ErrNotFound
	}
	return inst, nil
}

type guestBindingFixture struct {
	receiver     *spansWriterReceiver
	ingester     *GuestSpansIngester
	store        *guestBindingFakeStore
	account      string
	app          state.App
	deployment   string
	onA, onB     string
	migrated     string
	otherDeploy  string
	otherAccount state.App
	otherOnA     string
}

func newGuestBindingFixture(t *testing.T) *guestBindingFixture {
	t.Helper()
	account, outsider := uuid.New(), uuid.New()
	nodeA, nodeB := uuid.NewString(), uuid.NewString()
	f := &guestBindingFixture{
		account:      account.String(),
		app:          state.App{ID: uuid.NewString(), AccountID: account.String()},
		otherAccount: state.App{ID: uuid.NewString(), AccountID: outsider.String()},
		deployment:   uuid.NewString(),
		onA:          uuid.NewString(), onB: uuid.NewString(), migrated: uuid.NewString(),
		otherDeploy: uuid.NewString(), otherOnA: uuid.NewString(),
	}
	f.store = &guestBindingFakeStore{
		apps: map[string]state.App{f.app.ID: f.app, f.otherAccount.ID: f.otherAccount},
		instances: map[string]state.Instance{
			f.onA:         {ID: f.onA, AppID: f.app.ID, DeploymentID: f.deployment, NodeID: nodeA},
			f.onB:         {ID: f.onB, AppID: f.app.ID, DeploymentID: f.deployment, NodeID: nodeB},
			f.migrated:    {ID: f.migrated, AppID: f.app.ID, DeploymentID: f.deployment, NodeID: nodeB, MigratedFromNodeID: &nodeA},
			f.otherDeploy: {ID: f.otherDeploy, AppID: f.app.ID, DeploymentID: uuid.NewString(), NodeID: nodeA},
			f.otherOnA:    {ID: f.otherOnA, AppID: f.otherAccount.ID, DeploymentID: f.deployment, NodeID: nodeA},
		},
	}
	accounts := &fakeGuestSpansAccounts{plans: map[string]api.Plan{account.String(): api.PlanPro, outsider.String(): api.PlanPro}}
	f.ingester = NewGuestSpansIngester(accounts, peraccount.NewLimiter(), discardLogger()).
		WithLogs(state.NewMemStore()).
		WithPeerNodes(cnResolver{"compute-a": nodeA, "compute-b": nodeB}, f.store)
	f.receiver = NewSpansWriterReceiver(&fakeSpansWriterStore{}, nil, nil, true)
	f.receiver.guest = f.ingester
	return f
}

func (f *guestBindingFixture) frame(t *testing.T, account, app, deployment, instance string) *apidpb.IngestGuestSpansRequest {
	return &apidpb.IngestGuestSpansRequest{
		AccountId: account, AppId: app, DeploymentId: deployment, InstanceId: instance,
		Codec: uint32(api.TraceCodecProtobuf), Payload: guestSpansPayload(t, 1),
	}
}

func TestGuestIngestBindsFramesToTheCallersNode(t *testing.T) {
	f := newGuestBindingFixture(t)
	for _, tc := range []struct {
		name string
		req  *apidpb.IngestGuestSpansRequest
		want string
	}{
		{"its own instance", f.frame(t, f.account, f.app.ID, f.deployment, f.onA), guestSpansAccepted},
		{"migrated away from it", f.frame(t, f.account, f.app.ID, f.deployment, f.migrated), guestSpansAccepted},
		{"another node's instance", f.frame(t, f.account, f.app.ID, f.deployment, f.onB), guestSpansInvalid},
		{"another deployment's instance", f.frame(t, f.account, f.app.ID, f.deployment, f.otherDeploy), guestSpansInvalid},
		{"its instance under another app", f.frame(t, f.otherAccount.AccountID, f.otherAccount.ID, f.deployment, f.onA), guestSpansInvalid},
		{"its instance under another account", f.frame(t, f.otherAccount.AccountID, f.app.ID, f.deployment, f.onA), guestSpansInvalid},
		{"another account's app on its node", f.frame(t, f.account, f.otherAccount.ID, f.deployment, f.otherOnA), guestSpansInvalid},
		{"no such instance", f.frame(t, f.account, f.app.ID, f.deployment, uuid.NewString()), guestSpansInvalid},
		{"not an instance id", f.frame(t, f.account, f.app.ID, f.deployment, "vm-1"), guestSpansInvalid},
	} {
		t.Run(tc.name, func(t *testing.T) {
			resp, err := f.receiver.IngestGuestSpans(fromNode("compute-a"), tc.req)
			if err != nil || resp.GetOutcome() != tc.want {
				t.Fatalf("spans = %+v, %v; want %q", resp, err, tc.want)
			}
		})
	}

	logs := f.frame(t, f.account, f.app.ID, f.deployment, f.onB)
	logs.Payload = guestLogsPayload(t, guestLogRecord("boom", logspb.SeverityNumber_SEVERITY_NUMBER_ERROR, guestLogTraceID))
	if resp, err := f.receiver.IngestGuestLogs(fromNode("compute-a"), logs); err != nil || resp.GetOutcome() != guestSpansInvalid {
		t.Fatalf("logs for another node's instance = %+v, %v; want invalid", resp, err)
	}
	if resp, err := f.receiver.IngestGuestLogs(fromNode("compute-b"), logs); err != nil || resp.GetOutcome() != guestSpansAccepted {
		t.Fatalf("logs from the instance's node = %+v, %v; want accepted", resp, err)
	}

	own := f.frame(t, f.account, f.app.ID, f.deployment, f.onA)
	if _, err := f.receiver.IngestGuestSpans(fromNode("compute-z"), own); status.Code(err) != codes.Unauthenticated {
		t.Fatalf("unregistered node spans = %v, want Unauthenticated", err)
	}
	if _, err := f.receiver.IngestGuestLogs(fromNode("compute-z"), logs); status.Code(err) != codes.Unauthenticated {
		t.Fatalf("unregistered node logs = %v, want Unauthenticated", err)
	}
	// A local socket carries no certificate: vmmd on the same box.
	if resp, err := f.receiver.IngestGuestSpans(context.Background(), f.frame(t, f.account, f.app.ID, f.deployment, f.onB)); err != nil || resp.GetOutcome() != guestSpansAccepted {
		t.Fatalf("local socket = %+v, %v; want accepted", resp, err)
	}
	// Without a node resolver (single box) mTLS frames are not bound.
	unbound := NewSpansWriterReceiver(&fakeSpansWriterStore{}, nil, nil, true)
	unbound.guest = NewGuestSpansIngester(&fakeGuestSpansAccounts{plans: map[string]api.Plan{f.account: api.PlanPro}}, peraccount.NewLimiter(), discardLogger())
	if resp, err := unbound.IngestGuestSpans(fromNode("compute-a"), f.frame(t, f.account, f.app.ID, f.deployment, f.onB)); err != nil || resp.GetOutcome() != guestSpansAccepted {
		t.Fatalf("unbound apid = %+v, %v; want accepted", resp, err)
	}
}

func TestGuestIngestBindingCachesDecisions(t *testing.T) {
	f := newGuestBindingFixture(t)
	now := time.Date(2026, 10, 28, 12, 0, 0, 0, time.UTC)
	f.ingester.binding.now = func() time.Time { return now }
	ingest := func(instance string) string {
		t.Helper()
		resp, err := f.receiver.IngestGuestSpans(fromNode("compute-a"), f.frame(t, f.account, f.app.ID, f.deployment, instance))
		if err != nil {
			t.Fatal(err)
		}
		return resp.GetOutcome()
	}

	ingest(f.onA)
	reads := f.store.reads
	if got := ingest(f.onA); got != guestSpansAccepted || f.store.reads != reads {
		t.Fatalf("second frame = %q after %d reads, want accepted from the cache", got, f.store.reads-reads)
	}
	now = now.Add(guestBindingOwnTTL)
	if ingest(f.onA); f.store.reads == reads {
		t.Fatal("an expired decision was not looked up again")
	}

	// A refusal is kept only briefly: the instance may have just migrated.
	if got := ingest(f.onB); got != guestSpansInvalid {
		t.Fatalf("foreign = %q", got)
	}
	f.store.mu.Lock()
	inst := f.store.instances[f.onB]
	inst.NodeID = f.store.instances[f.onA].NodeID
	f.store.instances[f.onB] = inst
	f.store.mu.Unlock()
	if got := ingest(f.onB); got != guestSpansInvalid {
		t.Fatalf("cached refusal = %q", got)
	}
	now = now.Add(guestBindingForeignTTL)
	if got := ingest(f.onB); got != guestSpansAccepted {
		t.Fatalf("after the refusal expired = %q, want accepted", got)
	}

	// A failed lookup is retried, never cached as a refusal.
	f.store.err = errors.New("db down")
	fresh := uuid.NewString()
	if got := ingest(fresh); got != guestSpansUnavailable {
		t.Fatalf("lookup error = %q, want unavailable", got)
	}
	f.store.err = nil
	f.store.instances[fresh] = state.Instance{ID: fresh, AppID: f.app.ID, DeploymentID: f.deployment, NodeID: f.store.instances[f.onA].NodeID}
	if got := ingest(fresh); got != guestSpansAccepted {
		t.Fatalf("after the database recovered = %q, want accepted", got)
	}
}

func TestGuestIngestBindingStaysBounded(t *testing.T) {
	f := newGuestBindingFixture(t)
	now := time.Date(2026, 10, 28, 12, 0, 0, 0, time.UTC)
	b := f.ingester.binding
	b.now = func() time.Time { return now }
	for i := range guestBindingMaxEntries {
		b.decided[guestBindingKey{instance: strconv.Itoa(i)}] = guestBindingDecision{own: true, until: now.Add(time.Hour)}
	}
	ctx := fromNode("compute-a")
	if _, err := f.receiver.IngestGuestSpans(ctx, f.frame(t, f.account, f.app.ID, f.deployment, f.onA)); err != nil {
		t.Fatal(err)
	}
	if len(b.decided) != 1 {
		t.Fatalf("cache holds %d decisions after filling up, want only the new one", len(b.decided))
	}
}
