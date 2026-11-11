package receivers

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/requestanalytics"
	"github.com/onebox-faas/faas/pkg/state"
)

type durableRollupTestStore struct {
	fakeRollupStore
	windows   map[uuid.UUID]string
	markerErr error
}

func (s *durableRollupTestStore) RecordRequestAnalyticsWindow(_ context.Context, id, app uuid.UUID, at time.Time, kind string) error {
	if s.markerErr != nil {
		return s.markerErr
	}
	s.windows[id] = kind
	return nil
}

func (s *durableRollupTestStore) AddRequestAnalyticsRollups(ctx context.Context, rows []state.RequestAnalyticsRollup, coverage []state.RequestAnalyticsCoverage) error {
	if err := s.fakeRollupStore.AddRequestAnalyticsRollups(ctx, rows, coverage); err != nil {
		return err
	}
	for _, c := range coverage {
		if s.windows[c.PendingID] == "pending" {
			delete(s.windows, c.PendingID)
		}
	}
	return nil
}

func TestRollupsPersistPendingAndLostEvidence(t *testing.T) {
	store := &durableRollupTestStore{windows: map[uuid.UUID]string{}}
	a := NewRequestAnalyticsRollups(store, nil, nil)
	a.now = func() time.Time { return time.Now().Add(3 * time.Minute) }
	row := RequestAnalyticsRollupRow{EventID: uuid.New(), AccountID: uuid.New(), AppID: uuid.New(), DeploymentID: uuid.New(), ReceivedAt: time.Now().UTC(), Route: "GET /", Method: "GET", Status: 200, Count: 1}
	if counted, err := a.add(t.Context(), row); !counted || err != nil {
		t.Fatalf("add: %v %v", counted, err)
	}
	if len(store.windows) != 1 {
		t.Fatal("acknowledged without durable pending marker")
	}
	store.err = errors.New("database failure")
	if err := a.Flush(t.Context()); err == nil || len(store.windows) != 1 {
		t.Fatal("failed flush lost pending evidence")
	}
	store.err = nil
	if err := a.Flush(t.Context()); err != nil || len(store.windows) != 0 {
		t.Fatalf("successful flush retained pending marker: %v %+v", err, store.windows)
	}
	a.agg = requestanalytics.NewAggregator(1)
	a.agg.Add(requestanalytics.Row{AppID: uuid.New(), ReceivedAt: row.ReceivedAt, Route: "full", Method: "GET", Status: 200, Count: 1})
	row.EventID = uuid.New()
	if counted, err := a.add(t.Context(), row); counted || err != nil {
		t.Fatalf("overflow: %v %v", counted, err)
	}
	lost := false
	for _, kind := range store.windows {
		if kind == "lost" {
			lost = true
		}
	}
	if !lost {
		t.Fatal("overflow did not persist loss")
	}
	store.markerErr = errors.New("cannot persist loss")
	if _, err := a.add(t.Context(), row); err == nil {
		t.Fatal("overflow acknowledged despite failed loss persistence")
	}
}

func TestRollupUnicodeAndMethodsRemainBounded(t *testing.T) {
	store := &fakeRollupStore{}
	a := NewRequestAnalyticsRollups(store, nil, nil)
	a.now = func() time.Time { return time.Now().Add(3 * time.Minute) }
	for _, method := range []string{"X000", "X001", "GET"} {
		a.Add(RequestAnalyticsRollupRow{AppID: uuid.New(), ReceivedAt: time.Now(), Route: "__route_other__", Method: method, ReferrerHost: strings.Repeat("界", 171), Status: 200, Count: 1})
	}
	if err := a.Flush(t.Context()); err != nil {
		t.Fatal(err)
	}
	for _, r := range store.rows {
		if !utf8.ValidString(r.Dimension) || len(r.Dimension) > api.RequestAnalyticsDimensionMaxBytes {
			t.Fatalf("unsafe dimension: %q", r.Dimension)
		}
		if r.DimensionKind == RollupKindRoute && r.Method != "OTHER" && r.Method != "GET" {
			t.Fatalf("unbounded method: %s", r.Method)
		}
	}
	if got := requestanalytics.Dimension("bad\x00host", "empty"); strings.ContainsRune(got, 0) {
		t.Fatalf("NUL survived dimension normalization: %q", got)
	}
}

func TestUnsupportedMethodCountsWithoutInvalidRawInsert(t *testing.T) {
	raw := &consumerTelemetryStore{account: state.Account{Plan: api.PlanPro}}
	store := &fakeRollupStore{}
	a := NewRequestAnalyticsRollups(store, nil, nil)
	a.now = func() time.Time { return time.Now().Add(3 * time.Minute) }
	receiver := NewRequestTelemetryReceiver(raw, nil, nil, true)
	a.applyRequestTelemetry(receiver)
	req := rollupTelemetryRequest(uuid.NewString(), uuid.NewString(), uuid.NewString(), time.Now())
	req.Method = "X-CUSTOM"
	req.RouteTemplate = "__route_other__"
	out := receiver.HandleOne(t.Context(), req)
	if out.GetOutcome() != rtOutcomeNotRetained || len(raw.inserted) != 0 {
		t.Fatalf("unsupported raw insert: %s %+v", out.GetOutcome(), raw.inserted)
	}
	if err := a.Flush(t.Context()); err != nil {
		t.Fatal(err)
	}
	if got := store.requests(RollupKindRoute, ""); got != 1 {
		t.Fatalf("custom-method traffic lost: %d", got)
	}
	for _, r := range store.rows {
		if r.DimensionKind == RollupKindRoute && r.Method != "OTHER" {
			t.Fatalf("unbounded method %q", r.Method)
		}
	}
}
