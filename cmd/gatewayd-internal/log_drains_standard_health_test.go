package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/logdrain"
	"github.com/onebox-faas/faas/pkg/scheddgrpc"
	"github.com/onebox-faas/faas/pkg/state"
)

type healthGatewayTestStore interface {
	inventoryGatewayTestStore
	state.ApplicationStandardLogHealthStore
}

func TestStandardLogHealthProductionSender(t *testing.T) {
	standardLogHealthProductionSender(t, state.NewMemStore())
}

func standardLogHealthProductionSender(t *testing.T, s healthGatewayTestStore) {
	t.Helper()
	var code, requests atomic.Int64
	code.Store(http.StatusNoContent)
	endpoint := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		_, _ = io.Copy(io.Discard, r.Body)
		w.WriteHeader(int(code.Load()))
	}))
	defer endpoint.Close()
	f := newInventoryGatewayFixtureWithTarget(t, s, endpoint.URL)
	m := inventoryGatewayManager(t, f, &inventoryQuietLogs{})
	stream := newControllableScheddStream()
	m.resolver = &fixedScheddResolver{c: &controllableScheddClient{stream: stream}}
	m.httpClient = endpoint.Client()
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	defer stream.finish(io.EOF)
	m.reconcile(ctx)
	m.flushHealth(ctx)
	unknown := standardLogHealthGatewayFact(t, s, f, "unknown")
	if requests.Load() != 0 || unknown.SourceInstanceID != "" || unknown.Sequence != 0 {
		t.Fatal("quiet service fabricated a provider probe or delivery")
	}
	dep, err := s.CreateDeployment(ctx, state.Deployment{AppID: f.app.ID, Kind: state.DeploymentKindImage, Status: state.DeploySuperseded})
	if err != nil {
		t.Fatal(err)
	}
	ins, err := s.CreateInstance(ctx, f.app.ID, dep.ID, "stopped", 128, m.standardSession.NodeID, "")
	if err != nil {
		t.Fatal(err)
	}
	stream.pushFrame(scheddgrpc.LogFrame{InstanceID: ins.ID, Seq: 1, Stream: "stdout", Line: "private log contents", WrittenAt: time.Now()})
	o := m.workers[unknown.DrainID].standardHealth
	standardLogHealthWait(t, ctx, o, "healthy", 1)
	m.flushHealth(ctx)
	healthy := standardLogHealthGatewayFact(t, s, f, "healthy")
	if healthy.Sequence != 1 || healthy.SourceInstanceID != uuid.MustParse(ins.ID).String() {
		t.Fatal("actual delivery lost source identity")
	}
	m.flushHealth(ctx)
	heartbeat := standardLogHealthGatewayFact(t, s, f, "healthy")
	if heartbeat.EventAt != healthy.EventAt || !heartbeat.ObservedAt.After(healthy.ObservedAt) {
		t.Fatal("periodic refresh manufactured a new successful delivery")
	}
	code.Store(http.StatusInternalServerError)
	stream.pushFrame(scheddgrpc.LogFrame{InstanceID: ins.ID, Seq: 2, Stream: "stdout", Line: "second private log", WrittenAt: time.Now()})
	standardLogHealthWait(t, ctx, o, "degraded", 0)
	m.flushHealth(ctx)
	failed := standardLogHealthGatewayFact(t, s, f, "degraded")
	if failed.EventRevision <= healthy.EventRevision {
		t.Fatal("delivery failure did not supersede first success")
	}
	if _, err := s.RecordApplicationStandardLogHealth(ctx, m.standardSession, o.spec, healthy.ApplicationStandardLogHealthEvent); err == nil {
		t.Fatal("late first success replaced current failure")
	}
	code.Store(http.StatusNoContent)
	standardLogHealthWait(t, ctx, o, "healthy", 2)
	m.flushHealth(ctx)
	recovered := standardLogHealthGatewayFact(t, s, f, "healthy")
	if recovered.Sequence != 2 || !recovered.EventAt.After(failed.EventAt) {
		t.Fatal("successful retry did not repair transient provider failure")
	}
	stream.pushFrame(scheddgrpc.LogFrame{IsGap: true})
	standardLogHealthWait(t, ctx, o, "degraded", 0)
	m.flushHealth(ctx)
	if lost := standardLogHealthGatewayFact(t, s, f, "degraded"); lost.Reason != "source_gap" {
		t.Fatal("source gap did not reach persisted health")
	}
	stream.pushFrame(scheddgrpc.LogFrame{InstanceID: ins.ID, Seq: 3, Stream: "stdout", Line: "third private log", WrittenAt: time.Now()})
	standardLogHealthWaitDelivered(t, ctx, m, unknown.DrainID, 3)
	m.flushHealth(ctx)
	if lost := standardLogHealthGatewayFact(t, s, f, "degraded"); lost.Reason != "source_gap" {
		t.Fatal("later 2xx erased unrecovered source loss")
	}
	if _, err := s.RegisterApplicationStandardLogConsumer(ctx, m.standardSession.NodeID, uuid.NewString()); err != nil {
		t.Fatal(err)
	}
	m.flushStandardLogHealth(ctx)
	if !m.standardLogConsumerFenced() {
		t.Fatal("superseded gateway continued reporting health")
	}
	rows, err := s.ListApplicationStandardLogHealth(ctx, f.owner.PersonalOrg.ID, f.app.ID)
	if err != nil || len(rows) != 0 {
		t.Fatal("new startup inherited old process health")
	}
}

func standardLogHealthGatewayFact(t *testing.T, s healthGatewayTestStore, f inventoryGatewayFixture, status string) state.ApplicationStandardLogHealthObservation {
	t.Helper()
	rows, err := s.ListApplicationStandardLogHealth(t.Context(), f.owner.PersonalOrg.ID, f.app.ID)
	if err != nil || len(rows) != 1 || rows[0].Status != status {
		t.Fatalf("gateway health: %+v %v", rows, err)
	}
	e, err := s.GetApplicationStandardEnrollment(t.Context(), f.owner.PersonalOrg.ID, f.app.ID)
	if err != nil || e.State != "persisted" || e.ObservedRevision != 0 {
		t.Fatal("sender health advanced whole-app observation")
	}
	return rows[0]
}

func standardLogHealthWait(t *testing.T, ctx context.Context, o *appLogDrainStandardHealthObserver, status string, seq int64) {
	t.Helper()
	ticker := time.NewTicker(5 * time.Millisecond)
	defer ticker.Stop()
	for {
		o.mu.Lock()
		e := o.event
		o.mu.Unlock()
		if e.Status == status && e.Sequence == seq {
			return
		}
		select {
		case <-ticker.C:
		case <-ctx.Done():
			t.Fatalf("health callback missing, current: %+v", e)
		}
	}
}

func standardLogHealthWaitDelivered(t *testing.T, ctx context.Context, m *appLogDrainManager, drain string, count int64) {
	t.Helper()
	ticker := time.NewTicker(5 * time.Millisecond)
	defer ticker.Stop()
	for {
		m.healthMu.Lock()
		h := m.health[drain]
		m.healthMu.Unlock()
		if h.DeliveredTotal >= count {
			return
		}
		select {
		case <-ticker.C:
		case <-ctx.Done():
			t.Fatal("later actual log delivery missing")
		}
	}
}

func TestStandardLogHealthStickyLossAndExhaustion(t *testing.T) {
	m := newAppLogDrainManager(state.NewMemStore(), nil, nil, nil, nil)
	d := standardLogReceiptSpec("https://logs.example.com")
	for _, reason := range []string{"records_lost", "source_gap"} {
		t.Run(reason, func(t *testing.T) {
			o := m.newStandardLogHealthObserver(d)
			o.failed(reason, true)
			o.failed("delivery_failed", false)
			o.delivered(logdrain.Record{InstanceID: uuid.NewString(), Sequence: 1})
			if o.event.Status != "degraded" || o.event.Reason != reason {
				t.Fatal("later delivery repaired irreversible loss")
			}
		})
	}
	o := m.newStandardLogHealthObserver(d)
	o.event.EventRevision = api.ApplicationStandardMaxLogHealthEvent - 1
	o.delivered(logdrain.Record{InstanceID: uuid.NewString(), Sequence: 1})
	o.delivered(logdrain.Record{InstanceID: uuid.NewString(), Sequence: 2})
	if o.event.EventRevision != api.ApplicationStandardMaxLogHealthEvent || o.event.Status != "degraded" || o.event.Reason != "reporter_exhausted" {
		t.Fatal("event counter overflow revived healthy reporter")
	}
}

type delayedStandardHealthStore struct {
	*state.MemStore
	entered, release chan struct{}
}

func (s *delayedStandardHealthStore) RecordApplicationStandardLogHealth(ctx context.Context, c state.ApplicationStandardLogConsumerSession, d state.AppLogDrain, e state.ApplicationStandardLogHealthEvent) (state.ApplicationStandardLogHealthObservation, error) {
	if e.EventRevision == 2 {
		close(s.entered)
		select {
		case <-s.release:
		case <-ctx.Done():
			return state.ApplicationStandardLogHealthObservation{}, ctx.Err()
		}
	}
	return s.MemStore.RecordApplicationStandardLogHealth(ctx, c, d, e)
}

func TestStandardLogHealthRejectsReorderedWrites(t *testing.T) {
	s := &delayedStandardHealthStore{MemStore: state.NewMemStore(), entered: make(chan struct{}), release: make(chan struct{})}
	f := newInventoryGatewayFixture(t, s)
	m := inventoryGatewayManager(t, f, &inventoryQuietLogs{})
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	m.reconcile(ctx)
	m.flushStandardLogHealth(ctx)
	unknown := standardLogHealthGatewayFact(t, s, f, "unknown")
	dep, err := s.CreateDeployment(ctx, state.Deployment{AppID: f.app.ID, Kind: state.DeploymentKindImage, Status: state.DeploySuperseded})
	if err != nil {
		t.Fatal(err)
	}
	ins, err := s.CreateInstance(ctx, f.app.ID, dep.ID, "stopped", 128, m.standardSession.NodeID, "")
	if err != nil {
		t.Fatal(err)
	}
	o := m.workers[unknown.DrainID].standardHealth
	o.delivered(logdrain.Record{InstanceID: ins.ID, Sequence: 1})
	finished := make(chan error, 1)
	go func() { finished <- o.flush(ctx, s, m.standardSession) }()
	select {
	case <-s.entered:
	case <-ctx.Done():
		t.Fatal("first report did not enter storage")
	}
	o.failed("retrying", false)
	if err := o.flush(ctx, s, m.standardSession); err != nil {
		t.Fatal(err)
	}
	close(s.release)
	if err := <-finished; !errors.Is(err, state.ErrApplicationStandardLogHealthStale) {
		t.Fatalf("reordered success accepted: %v", err)
	}
	if got := standardLogHealthGatewayFact(t, s, f, "degraded"); got.EventRevision != 3 || got.Reason != "retrying" {
		t.Fatal("late write changed current event")
	}
}

func TestStandardLogHealthEnqueueRejectionCanRecover(t *testing.T) {
	endpoint := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer endpoint.Close()
	d := standardLogReceiptSpec(endpoint.URL)
	m := newAppLogDrainManager(state.NewMemStore(), nil, nil, nil, nil)
	o := m.newStandardLogHealthObserver(d)
	r := logdrain.Record{AppID: d.AppID, AccountID: d.AccountID, InstanceID: uuid.NewString(), Sequence: 1, Line: strings.Repeat("private", 100)}
	raw, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	q, err := logdrain.NewQueue(logdrain.QueueConfig{Root: t.TempDir(), DrainID: d.ID, MaxBytes: int64(len(raw) * 2)})
	if err != nil {
		t.Fatal(err)
	}
	sender, err := logdrain.New(logdrain.Config{Kind: logdrain.KindHTTPJSON, TargetURL: endpoint.URL, HTTPClient: endpoint.Client(), DurableQueue: q, OnDropped: o.rejected, OnDelivered: o.delivered})
	if err != nil {
		t.Fatal(err)
	}
	if !sender.Enqueue(r) {
		t.Fatal("first record did not fit test outbox")
	}
	r.Sequence = 2
	if sender.Enqueue(r) || o.event.Status != "degraded" || o.event.Reason != "queue_fault" || o.loss != "" {
		t.Fatal("queue rejection claimed unrecoverable source loss")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	done := make(chan struct{})
	go func() { sender.Run(ctx); close(done) }()
	defer func() { cancel(); <-done }()
	standardLogHealthWait(t, ctx, o, "healthy", 1)
	if !sender.Enqueue(r) {
		t.Fatal("rejected source sequence was consumed instead of retained")
	}
}
