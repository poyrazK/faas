package gateway

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

type sharedMirrorSlotLeaseFake struct {
	mu     sync.Mutex
	next   int
	active map[string]string
	err    error
}

func (s *sharedMirrorSlotLeaseFake) TryAcquireMirrorSlotLease(_ context.Context, ruleID string, limit int, _ time.Duration) (string, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.err != nil {
		return "", false, s.err
	}
	if s.active == nil {
		s.active = make(map[string]string)
	}
	active := 0
	for _, activeRuleID := range s.active {
		if activeRuleID == ruleID {
			active++
		}
	}
	if active >= limit {
		return "", false, nil
	}
	s.next++
	leaseID := fmt.Sprintf("lease-%d", s.next)
	s.active[leaseID] = ruleID
	return leaseID, true, nil
}

func (s *sharedMirrorSlotLeaseFake) ReleaseMirrorSlotLease(_ context.Context, ruleID, leaseID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.active[leaseID] == ruleID {
		delete(s.active, leaseID)
	}
	return nil
}

func TestMirrorSlotLeaseCoordinatesHandlers(t *testing.T) {
	t.Parallel()
	store := &sharedMirrorSlotLeaseFake{}
	first := &Handler{MirrorMaxConcurrentPerRule: 1, mirrorSlotLeaseStore: store}
	second := &Handler{MirrorMaxConcurrentPerRule: 1, mirrorSlotLeaseStore: store}

	releaseFirst, acquired, err := first.acquireMirrorSlot(context.Background(), "rule-1")
	if err != nil || !acquired {
		t.Fatalf("first handler acquire = (%v, %v), want (true, nil)", acquired, err)
	}
	if release, acquired, err := second.acquireMirrorSlot(context.Background(), "rule-1"); err != nil || acquired || release != nil {
		t.Fatalf("second handler acquire: acquired=%v err=%v, want no permit", acquired, err)
	}
	releaseFirst()
	releaseFirst() // deferred release is safe to retry

	releaseSecond, acquired, err := second.acquireMirrorSlot(context.Background(), "rule-1")
	if err != nil || !acquired {
		t.Fatalf("acquire after release = (%v, %v), want (true, nil)", acquired, err)
	}
	releaseSecond()
}

func TestMirrorSlotLeaseStoreFailureDoesNotFallBackToLocalCap(t *testing.T) {
	t.Parallel()
	wantErr := errors.New("postgres unavailable")
	h := &Handler{
		MirrorMaxConcurrentPerRule: 1,
		mirrorSlotLeaseStore:       &sharedMirrorSlotLeaseFake{err: wantErr},
	}
	if release, acquired, err := h.acquireMirrorSlot(context.Background(), "rule-1"); !errors.Is(err, wantErr) || acquired || release != nil {
		t.Fatalf("shared acquire: acquired=%v err=%v, want no permit and postgres error", acquired, err)
	}
	if !h.tryAcquireMirrorSlot("rule-1") {
		t.Fatal("shared-store failure consumed a process-local slot")
	}
	h.releaseMirrorSlot("rule-1")
}

func TestDispatchMirrorRetainsSharedSlotLeaseWhenParkingFails(t *testing.T) {
	for _, tc := range []struct {
		name         string
		parkErr      error
		wantRetained bool
	}{
		{name: "park succeeds"},
		{name: "park fails", parkErr: errors.New("scheduler unavailable"), wantRetained: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := &sharedMirrorSlotLeaseFake{}
			base := &mirrorFakeBackend{}
			backend := &mirrorParkerBackend{
				mirrorTargetFakeBackend: &mirrorTargetFakeBackend{
					mirrorFakeBackend: base,
					target:            Target{AppID: "app-1", NodeID: "node", InstanceID: "shadow", DeploymentID: "mirror-deployment"},
				},
				parkErr: tc.parkErr,
			}
			h := &Handler{
				backend:                    backend,
				log:                        slog.New(slog.NewTextHandler(io.Discard, nil)),
				MirrorMaxConcurrentPerRule: 1,
				mirrorSlotLeaseStore:       store,
			}
			capture := newMirrorSourceCapture()
			capture.writeHeader(http.StatusOK)
			capture.complete()
			h.dispatchMirror(context.Background(), "source", nil,
				MirrorRuleRow{ID: "rule-1", AppID: "app-1", MirrorDeploymentID: "mirror-deployment"},
				nil, nil, "request-1", capture)

			store.mu.Lock()
			retained := len(store.active) == 1
			store.mu.Unlock()
			if retained != tc.wantRetained {
				t.Fatalf("lease retained = %v, want %v", retained, tc.wantRetained)
			}
		})
	}
}

func TestDispatchMirror_SharedSlotStoreFailureSkipsScheduler(t *testing.T) {
	t.Parallel()
	backend := &mirrorFakeBackend{}
	h := NewHandlerWith(backend, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	h.WithMirrorSlotLeaseStore(&sharedMirrorSlotLeaseFake{err: errors.New("postgres unavailable")})
	source := newMirrorSourceCapture()
	source.writeHeader(http.StatusOK)
	source.write([]byte(`{"ok":true}`))
	source.complete()
	rule := MirrorRuleRow{ID: "rule-1", AppID: "app-1", AccountID: "acct-1", MirrorDeploymentID: "mirror-deployment", Percent: 100}
	req := httptest.NewRequest(http.MethodGet, "http://example.test/export", nil)

	h.dispatchMirror(context.Background(), "source-instance", nil, rule, req, nil, "request-1", source)
	if got := backend.scheduleCalls.Load(); got != 0 {
		t.Fatalf("scheduler calls after shared slot failure = %d, want 0", got)
	}
}
