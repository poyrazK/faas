package receivers

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/peer"
	"google.golang.org/grpc/status"

	apidpb "github.com/onebox-faas/faas/api/proto/onebox/faas/apid/v1"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// adr: 20261018083536 — vmmd's runtime warn/error lines land in the ledger,
// redacted, tenant-scoped by apid, capped per account, idempotent on replay.

type runtimeLogFakeStore struct {
	ledger    *state.MemStore
	apps      map[string]state.App
	appsErr   error
	instances map[string]state.Instance
}

func (f *runtimeLogFakeStore) InstanceByID(_ context.Context, id string) (state.Instance, error) {
	inst, ok := f.instances[id]
	if !ok {
		return state.Instance{}, state.ErrNotFound
	}
	return inst, nil
}

func (f *runtimeLogFakeStore) AppByID(_ context.Context, id string) (state.App, error) {
	if f.appsErr != nil {
		return state.App{}, f.appsErr
	}
	app, ok := f.apps[id]
	if !ok {
		return state.App{}, state.ErrNotFound
	}
	return app, nil
}

func (f *runtimeLogFakeStore) InsertLogEvent(ctx context.Context, event state.LogEvent) (state.LogEvent, error) {
	return f.ledger.InsertLogEvent(ctx, event)
}

func newRuntimeLogFixture(t *testing.T) (*RuntimeLogEventsReceiver, *runtimeLogFakeStore, state.App) {
	t.Helper()
	app := state.App{ID: uuid.NewString(), AccountID: uuid.NewString(), Slug: "shop"}
	store := &runtimeLogFakeStore{ledger: state.NewMemStore(), apps: map[string]state.App{app.ID: app}}
	return NewRuntimeLogEventsReceiver(store, true, nil, discardLogger()), store, app
}

func runtimeLine(appID, id, level, message string) *apidpb.RuntimeLogEvent {
	return &apidpb.RuntimeLogEvent{
		AppId: appID, DeploymentId: uuid.NewString(), InstanceId: "vm-1", SourceEventId: id,
		OccurredAtUnixMs: time.Now().Add(-time.Minute).UnixMilli(), Level: level, Stream: "stderr", Message: message,
	}
}

func ledgerRows(t *testing.T, store *runtimeLogFakeStore, app state.App) []state.LogEvent {
	t.Helper()
	rows, _, err := store.ledger.ListLogEvents(context.Background(), state.LogEventFilter{
		AccountID: app.AccountID, AppID: app.ID, Source: state.LogEventSourceRuntime,
		Since: time.Now().Add(-time.Hour), Until: time.Now().Add(time.Minute), Limit: 200,
	})
	if err != nil {
		t.Fatalf("list ledger: %v", err)
	}
	return rows
}

func TestRecordRuntimeLogEventsInsertsRedactsAndRejects(t *testing.T) {
	r, store, app := newRuntimeLogFixture(t)
	resp, err := r.RecordRuntimeLogEvents(context.Background(), &apidpb.RecordRuntimeLogEventsRequest{Events: []*apidpb.RuntimeLogEvent{
		runtimeLine(app.ID, "vm-1:1", "error", "payment failed for jane@example.com\n"),
		runtimeLine(app.ID, "vm-1:2", "warn", "slow query"),
		runtimeLine(app.ID, "vm-1:3", "info", "listening"),            // level outside the projection
		runtimeLine(uuid.NewString(), "vm-9:1", "error", "other app"), // unknown app
		runtimeLine(app.ID, "", "error", "no replay key"),
		runtimeLine(app.ID, "vm-1:4", "error", strings.Repeat("x", api.RuntimeLogEventMessageMaxBytes+1)),
	}})
	if err != nil {
		t.Fatalf("record: %v", err)
	}
	if resp.GetInserted() != 2 || resp.GetRejected() != 4 || resp.GetRateLimited() != 0 {
		t.Fatalf("response = %+v, want 2 inserted, 4 rejected", resp)
	}
	rows := ledgerRows(t, store, app)
	if len(rows) != 2 {
		t.Fatalf("ledger rows = %d, want 2", len(rows))
	}
	byID := map[string]state.LogEvent{}
	for _, row := range rows {
		byID[row.SourceEventID] = row
	}
	paid := byID["vm-1:1"]
	if strings.Contains(paid.Message, "jane@example.com") || paid.Level != "error" || paid.Stream != "stderr" || paid.InstanceID != "vm-1" {
		t.Fatalf("redacted row = %+v", paid)
	}
	if !strings.Contains(string(paid.Fields), `"email"`) {
		t.Fatalf("fields = %s, want the redaction names", paid.Fields)
	}
	if byID["vm-1:2"].Message != "slow query" {
		t.Fatalf("warn row = %+v", byID["vm-1:2"])
	}
}

func TestRecordRuntimeLogEventsReplayConverges(t *testing.T) {
	r, store, app := newRuntimeLogFixture(t)
	line := runtimeLine(app.ID, "vm-1:7", "error", "boom")
	for range 2 {
		if _, err := r.RecordRuntimeLogEvents(context.Background(), &apidpb.RecordRuntimeLogEventsRequest{Events: []*apidpb.RuntimeLogEvent{line}}); err != nil {
			t.Fatalf("record: %v", err)
		}
	}
	if rows := ledgerRows(t, store, app); len(rows) != 1 {
		t.Fatalf("ledger rows after replay = %d, want 1", len(rows))
	}
}

func TestRecordRuntimeLogEventsCapsTheAccount(t *testing.T) {
	r, _, app := newRuntimeLogFixture(t)
	frozen := time.Now()
	r.limiter.SetClock(func() time.Time { return frozen }) // no refill mid-test
	var inserted, limited int32
	for batch := 0; batch*api.RuntimeLogEventsBatchMax <= api.RuntimeLogEventsPerAccountMinute; batch++ {
		events := make([]*apidpb.RuntimeLogEvent, api.RuntimeLogEventsBatchMax)
		for i := range events {
			events[i] = runtimeLine(app.ID, uuid.NewString(), "error", "boom")
		}
		resp, err := r.RecordRuntimeLogEvents(context.Background(), &apidpb.RecordRuntimeLogEventsRequest{Events: events})
		if err != nil {
			t.Fatalf("record: %v", err)
		}
		inserted += resp.GetInserted()
		limited += resp.GetRateLimited()
	}
	if inserted != api.RuntimeLogEventsPerAccountMinute || limited == 0 {
		t.Fatalf("inserted %d limited %d, want the %d-line account cap", inserted, limited, api.RuntimeLogEventsPerAccountMinute)
	}
}

func TestRecordRuntimeLogEventsRefusals(t *testing.T) {
	r, store, app := newRuntimeLogFixture(t)
	tooMany := make([]*apidpb.RuntimeLogEvent, api.RuntimeLogEventsBatchMax+1)
	for i := range tooMany {
		tooMany[i] = runtimeLine(app.ID, uuid.NewString(), "error", "boom")
	}
	disabled := NewRuntimeLogEventsReceiver(store, false, nil, discardLogger())
	one := []*apidpb.RuntimeLogEvent{runtimeLine(app.ID, "vm-1:1", "error", "boom")}
	for name, tc := range map[string]struct {
		receiver *RuntimeLogEventsReceiver
		events   []*apidpb.RuntimeLogEvent
		appsErr  error
		want     codes.Code
	}{
		"disabled":        {receiver: disabled, events: one, want: codes.Unavailable},
		"oversized batch": {receiver: r, events: tooMany, want: codes.InvalidArgument},
		"store down":      {receiver: r, events: one, appsErr: errors.New("connection refused"), want: codes.Unavailable},
	} {
		t.Run(name, func(t *testing.T) {
			store.appsErr = tc.appsErr
			t.Cleanup(func() { store.appsErr = nil })
			_, err := tc.receiver.RecordRuntimeLogEvents(context.Background(), &apidpb.RecordRuntimeLogEventsRequest{Events: tc.events})
			if status.Code(err) != tc.want {
				t.Fatalf("code = %v (%v), want %v", status.Code(err), err, tc.want)
			}
		})
	}
}

// cnResolver maps certificate CNs to compute node IDs, as apid's node
// verifier does.
type cnResolver map[string]string

func (c cnResolver) NodeIDByCN(cn string) (string, error) {
	if id, ok := c[cn]; ok {
		return id, nil
	}
	return "", errors.New("unknown node")
}

// fromNode is a request context whose mutual-TLS peer certificate names cn.
func fromNode(cn string) context.Context {
	leaf := &x509.Certificate{Subject: pkix.Name{CommonName: cn}}
	return peer.NewContext(context.Background(), &peer.Peer{AuthInfo: credentials.TLSInfo{
		State: tls.ConnectionState{VerifiedChains: [][]*x509.Certificate{{leaf}}},
	}})
}

// adr: 958 — on the shared mTLS listener a node writes log lines only for
// instances it runs (or ran before a migration); a local socket and a
// single-box apid without a node resolver accept them as before.
func TestRecordRuntimeLogEventsBindsLinesToTheCallersNode(t *testing.T) {
	r, store, app := newRuntimeLogFixture(t)
	nodeA, nodeB := uuid.NewString(), uuid.NewString()
	onA, onB, migrated := uuid.NewString(), uuid.NewString(), uuid.NewString()
	store.instances = map[string]state.Instance{
		onA:      {ID: onA, AppID: app.ID, NodeID: nodeA},
		onB:      {ID: onB, AppID: app.ID, NodeID: nodeB},
		migrated: {ID: migrated, AppID: app.ID, NodeID: nodeB, MigratedFromNodeID: &nodeA},
	}
	r.WithPeerNodes(cnResolver{"compute-a": nodeA, "compute-b": nodeB})
	line := func(appID, instance, id string) *apidpb.RuntimeLogEvent {
		ev := runtimeLine(appID, id, "error", "boom")
		ev.InstanceId = instance
		return ev
	}
	other := state.App{ID: uuid.NewString(), AccountID: uuid.NewString()}
	store.apps[other.ID] = other
	batch := []*apidpb.RuntimeLogEvent{
		line(app.ID, onA, "a:1"),              // its own instance
		line(app.ID, migrated, "a:2"),         // migrated away from it
		line(app.ID, onB, "b:1"),              // another node's instance
		line(other.ID, onA, "x:1"),            // its instance, another app's id
		line(app.ID, uuid.NewString(), "u:1"), // no such instance
		line(app.ID, "vm-1", "v:1"),           // not an instance id
	}
	resp, err := r.RecordRuntimeLogEvents(fromNode("compute-a"), &apidpb.RecordRuntimeLogEventsRequest{Events: batch})
	if err != nil || resp.GetInserted() != 2 || resp.GetRejected() != 4 {
		t.Fatalf("node A = %+v, %v; want 2 inserted, 4 rejected", resp, err)
	}
	got := map[string]bool{}
	for _, row := range ledgerRows(t, store, app) {
		got[row.SourceEventID] = true
	}
	if !got["a:1"] || !got["a:2"] || got["b:1"] || got["u:1"] {
		t.Fatalf("ledger = %v, want node A's own instances only", got)
	}
	if _, err := r.RecordRuntimeLogEvents(fromNode("compute-z"), &apidpb.RecordRuntimeLogEventsRequest{Events: batch[:1]}); status.Code(err) != codes.Unauthenticated {
		t.Fatalf("unregistered node = %v, want Unauthenticated", err)
	}
	// A local socket carries no certificate: vmmd on the same box.
	if resp, err := r.RecordRuntimeLogEvents(context.Background(), &apidpb.RecordRuntimeLogEventsRequest{Events: []*apidpb.RuntimeLogEvent{line(app.ID, onB, "b:2")}}); err != nil || resp.GetInserted() != 1 {
		t.Fatalf("local socket = %+v, %v; want accepted", resp, err)
	}
}
