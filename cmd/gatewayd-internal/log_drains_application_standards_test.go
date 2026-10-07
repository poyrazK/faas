package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/logdrain"
	"github.com/onebox-faas/faas/pkg/scheddgrpc"
	"github.com/onebox-faas/faas/pkg/state"
)

type standardLogReceiptTestStore struct {
	mu       sync.Mutex
	drain    state.AppLogDrain
	calls    int
	err      error
	source   string
	sequence uint64
	binding  state.ApplicationStandardLogDrainBinding
}

func (s *standardLogReceiptTestStore) ListEnabledAppLogDrains(context.Context) ([]state.AppLogDrain, error) {
	return []state.AppLogDrain{s.drain}, nil
}
func (s *standardLogReceiptTestStore) ListApplicationStandardLogDeliveries(context.Context, string, string) ([]state.ApplicationStandardLogDeliveryObservation, error) {
	return nil, nil
}
func (s *standardLogReceiptTestStore) RecordApplicationStandardLogDelivery(_ context.Context, d state.AppLogDrain, source string, seq uint64) (state.ApplicationStandardLogDeliveryObservation, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls++
	s.source, s.sequence, s.binding = source, seq, *d.StandardBinding
	return state.ApplicationStandardLogDeliveryObservation{}, s.err
}

func standardLogReceiptSpec(target string) state.AppLogDrain {
	d := state.AppLogDrain{ID: uuid.NewString(), AppID: uuid.NewString(), AccountID: uuid.NewString(), Kind: state.AppLogDrainKindHTTPJSON, TargetURL: target, Enabled: true}
	d.StandardBinding = &state.ApplicationStandardLogDrainBinding{OrgID: uuid.NewString(), AppID: d.AppID, DrainID: d.ID, ResourceID: uuid.NewString(), DesiredRevision: 3,
		EffectiveHash: strings.Repeat("a", 64), ResourceConfigHash: strings.Repeat("b", 64), DrainConfigHash: state.ApplicationStandardLogDrainConfigHash(d)}
	return d
}

func TestStandardLogReceiptRetriesAndDoesNotRetainLog(t *testing.T) {
	d := standardLogReceiptSpec("https://logs.example.com")
	store := &standardLogReceiptTestStore{drain: d, err: errors.New("secret internal URL and credentials")}
	var logs bytes.Buffer
	m := newAppLogDrainManager(store, nil, nil, nil, slog.New(slog.NewTextHandler(&logs, nil)))
	o := m.newStandardLogObserver(d)
	m.workers[d.ID] = &appLogDrainWorker{spec: d, standardObserver: o}
	source := uuid.NewString()
	o.delivered(logdrain.Record{InstanceID: source, Sequence: 7, Line: "private log contents"})
	m.flushHealth(t.Context()) // The receipt seam works without a health store.
	if o.done || store.calls != 1 {
		t.Fatal("transient failure discarded receipt")
	}
	if strings.Contains(logs.String(), "secret internal") || strings.Contains(logs.String(), "private log") {
		t.Fatal("receipt log exposed source material")
	}
	store.err = nil
	m.flushHealth(t.Context())
	if !o.done || store.calls != 2 || store.source != source || store.sequence != 7 || store.binding != *d.StandardBinding {
		t.Fatal("retry lost exact receipt identity")
	}
	for i := 0; i < 20; i++ {
		o.delivered(logdrain.Record{InstanceID: source, Sequence: uint64(i + 8)})
		m.flushHealth(t.Context())
	}
	if store.calls != 2 {
		t.Fatal("accepted projection wrote a receipt per log line")
	}
}

func TestStandardLogReceiptStaleProjectionAndRefresh(t *testing.T) {
	d := standardLogReceiptSpec("https://logs.example.com")
	s := &standardLogReceiptTestStore{drain: d, err: state.ErrApplicationStandardLogDeliveryStale}
	m := newAppLogDrainManager(s, nil, nil, nil, nil)
	o := m.newStandardLogObserver(d)
	m.workers[d.ID] = &appLogDrainWorker{spec: d, standardObserver: o}
	o.delivered(logdrain.Record{InstanceID: uuid.NewString(), Sequence: 1})
	m.flushStandardLogDeliveries(t.Context())
	m.flushStandardLogDeliveries(t.Context())
	if s.calls != 1 || o.done || o.sequence != 0 {
		t.Fatal("stale worker kept retrying")
	}
	s.err = nil
	o.delivered(logdrain.Record{InstanceID: uuid.NewString(), Sequence: 2})
	m.flushStandardLogDeliveries(t.Context())
	if !o.done || s.calls != 2 || s.sequence != 2 {
		t.Fatal("rejected source suppressed later valid delivery")
	}
	newer := d
	b := *d.StandardBinding
	b.DesiredRevision++
	newer.StandardBinding = &b
	if sameAppLogDrainSpec(d, newer) || !sameAppLogDrainSpec(d, d) {
		t.Fatal("binding change did not change loaded worker spec")
	}
	legacy := d
	legacy.StandardBinding = nil
	if m.newStandardLogObserver(legacy) != nil {
		t.Fatal("legacy drain acquired standards authority")
	}
}

type blockedStandardLogReceiptStore struct {
	*standardLogReceiptTestStore
	entered, release chan struct{}
	once             sync.Once
}

func (s *blockedStandardLogReceiptStore) RecordApplicationStandardLogDelivery(ctx context.Context, d state.AppLogDrain, source string, seq uint64) (state.ApplicationStandardLogDeliveryObservation, error) {
	r, err := s.standardLogReceiptTestStore.RecordApplicationStandardLogDelivery(ctx, d, source, seq)
	s.once.Do(func() {
		close(s.entered)
		select {
		case <-s.release:
		case <-ctx.Done():
		}
	})
	return r, err
}

func TestStandardLogReceiptKeepsNewDeliveryDuringRejectedWrite(t *testing.T) {
	d := standardLogReceiptSpec("https://logs.example.com")
	store := &blockedStandardLogReceiptStore{standardLogReceiptTestStore: &standardLogReceiptTestStore{drain: d, err: state.ErrApplicationStandardLogDeliveryStale}, entered: make(chan struct{}), release: make(chan struct{})}
	m := newAppLogDrainManager(store, nil, nil, nil, nil)
	o := m.newStandardLogObserver(d)
	o.delivered(logdrain.Record{InstanceID: uuid.NewString(), Sequence: 1})
	finished := make(chan struct{})
	go func() { o.flush(t.Context(), store, m.log); close(finished) }()
	<-store.entered
	source := uuid.NewString()
	o.delivered(logdrain.Record{InstanceID: source, Sequence: 2})
	close(store.release)
	<-finished
	if o.sequence != 2 || o.source != source || o.done {
		t.Fatal("rejected write erased newer successful delivery")
	}
	store.err = nil
	o.flush(t.Context(), store, m.log)
	if !o.done || store.sequence != 2 || store.source != source {
		t.Fatal("newer delivery could not be acknowledged")
	}
}

func TestStandardLogReceiptProductionSender(t *testing.T) {
	for _, code := range []int{http.StatusNoContent, http.StatusInternalServerError} {
		t.Run(http.StatusText(code), func(t *testing.T) { standardLogReceiptProductionSender(t, code) })
	}
}

func TestStandardLogWorkerRetirementBeforeReplacement(t *testing.T) {
	d := standardLogReceiptSpec("https://logs.example.com")
	newer := d
	binding := *d.StandardBinding
	binding.DesiredRevision++
	newer.StandardBinding = &binding
	store := &standardLogReceiptTestStore{drain: newer}
	stream := newControllableScheddStream()
	m := newAppLogDrainManager(store, &fixedScheddResolver{c: &controllableScheddClient{stream: stream}}, nil, nil, nil)
	m.spoolRoot = t.TempDir()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	defer stream.finish(io.EOF)
	defer m.stopAll()
	canceled := 0
	old := &appLogDrainWorker{spec: d, cancel: func() { canceled++ }, done: make(chan struct{})}
	m.workers[d.ID] = old
	m.setActiveLocked(d, 1)
	m.reconcile(ctx)
	m.reconcile(ctx)
	if m.workers[d.ID] != old || !old.stopping || canceled != 1 || len(m.active) != 0 {
		t.Fatal("replacement reopened queue before previous loops exited")
	}
	close(old.done)
	m.reconcile(ctx)
	current := m.workers[d.ID]
	if current == nil || current == old || current.spec.StandardBinding.DesiredRevision != binding.DesiredRevision {
		t.Fatal("exited worker prevented replacement")
	}
}

func standardLogReceiptProductionSender(t *testing.T, code int) {
	t.Helper()
	posted := make(chan struct{}, 1)
	endpoint := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.Copy(io.Discard, r.Body)
		w.WriteHeader(code)
		select {
		case posted <- struct{}{}:
		default:
		}
	}))
	defer endpoint.Close()
	d := standardLogReceiptSpec(endpoint.URL)
	s := &standardLogReceiptTestStore{drain: d}
	stream := newControllableScheddStream()
	m := newAppLogDrainManager(s, &fixedScheddResolver{c: &controllableScheddClient{stream: stream}}, nil, nil, nil)
	m.spoolRoot = t.TempDir()
	m.httpClient = endpoint.Client()
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	defer stream.finish(io.EOF)
	defer m.stopAll()
	m.reconcile(ctx)
	instance := uuid.NewString()
	stream.pushFrame(scheddgrpc.LogFrame{InstanceID: instance, Seq: 7, Stream: "stdout", Line: "private log contents", WrittenAt: time.Now()})
	select {
	case <-posted:
	case <-ctx.Done():
		t.Fatal("sender did not post actual log")
	}
	o := m.workers[d.ID].standardObserver
	standardLogReceiptWaitForOutcome(t, ctx, m, d.ID, code)
	m.flushStandardLogDeliveries(ctx)
	if code == http.StatusNoContent && (s.calls != 1 || s.source != instance || s.sequence != 7 || !o.done) {
		t.Fatalf("2xx did not produce exact receipt: %+v", s)
	}
	if code == http.StatusInternalServerError && s.calls != 0 {
		t.Fatal("unsuccessful delivery produced receipt")
	}
}

func standardLogReceiptWaitForOutcome(t *testing.T, ctx context.Context, m *appLogDrainManager, id string, code int) {
	t.Helper()
	ticker := time.NewTicker(5 * time.Millisecond)
	defer ticker.Stop()
	for {
		m.healthMu.Lock()
		h := m.health[id]
		m.healthMu.Unlock()
		if code == http.StatusNoContent && h.DeliveredTotal > 0 || code != http.StatusNoContent && h.FailedTotal > 0 {
			return
		}
		select {
		case <-ticker.C:
		case <-ctx.Done():
			t.Fatal("delivery callback did not arrive")
		}
	}
}
