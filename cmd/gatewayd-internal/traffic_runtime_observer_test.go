// adr: 375
package main

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

type runtimeObserverTestStore struct {
	mu                                 sync.Mutex
	reads, registers, reports, retires int
	expected                           []int64
	boots                              []string
	features                           state.GatewayTrafficFeatures
	registerErrors                     []error
	reportError                        error
	events                             chan string
	deadlineMissing                    bool
}

func (s *runtimeObserverTestStore) record(ctx context.Context, event string) {
	d, ok := ctx.Deadline()
	if !ok || time.Until(d) > api.TrafficRuntimeObservationTimeout {
		s.deadlineMissing = true
	}
	s.events <- event
}

func (s *runtimeObserverTestStore) ReadGatewayTrafficEpoch(ctx context.Context, node string) (state.GatewayTrafficEpoch, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.reads++
	s.record(ctx, "read")
	return state.GatewayTrafficEpoch{NodeName: node, Generation: 7}, nil
}

func (s *runtimeObserverTestStore) RegisterGatewayTrafficEpoch(ctx context.Context, node, boot string, expected int64) (state.GatewayTrafficEpoch, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.registers++
	s.expected = append(s.expected, expected)
	s.boots = append(s.boots, boot)
	s.record(ctx, "register")
	if len(s.registerErrors) > 0 {
		err := s.registerErrors[0]
		s.registerErrors = s.registerErrors[1:]
		if err != nil {
			return state.GatewayTrafficEpoch{}, err
		}
	}
	return state.GatewayTrafficEpoch{NodeName: node, BootID: boot, Generation: 8}, nil
}

func (s *runtimeObserverTestStore) ReportGatewayTrafficRuntime(ctx context.Context, _ state.GatewayTrafficEpoch, features state.GatewayTrafficFeatures) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.reports++
	s.features = features
	s.record(ctx, "report")
	return s.reportError
}

func (s *runtimeObserverTestStore) RetireGatewayTrafficRuntime(ctx context.Context, _ state.GatewayTrafficEpoch) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.retires++
	s.record(ctx, "retire")
	return nil
}

func waitRuntimeObserverEvent(t *testing.T, events <-chan string, want string) {
	t.Helper()
	timer := time.NewTimer(5 * time.Second)
	defer timer.Stop()
	for {
		select {
		case event := <-events:
			if event == want {
				return
			}
		case <-timer.C:
			t.Fatalf("observer did not emit %s", want)
		}
	}
}

func TestTrafficRuntimeObserverBoundsReportingAndRetiresOnStop(t *testing.T) {
	store := &runtimeObserverTestStore{events: make(chan string, 20)}
	features := state.GatewayTrafficFeatures{RateCounterMode: "central", RetryCounterMode: "shared", RetryBackendID: "0123456789abcdef"}
	stop := startTrafficRuntimeObserver(t.Context(), store, "node", features, func() bool { return true }, discardLogger())
	t.Cleanup(stop)
	waitRuntimeObserverEvent(t, store.events, "report")
	stop()
	store.mu.Lock()
	defer store.mu.Unlock()
	if store.reports != 1 || store.retires != 1 || store.features != features || store.deadlineMissing {
		t.Fatalf("observer = %+v", store)
	}
}

func TestTrafficRuntimeObserverDoesNotPublishBeforeReady(t *testing.T) {
	store := &runtimeObserverTestStore{events: make(chan string, 20)}
	stop := startTrafficRuntimeObserver(t.Context(), store, "node", state.GatewayTrafficFeatures{RateCounterMode: "local", RetryCounterMode: "local"}, func() bool { return false }, discardLogger())
	t.Cleanup(stop)
	waitRuntimeObserverEvent(t, store.events, "retire")
	stop()
	store.mu.Lock()
	defer store.mu.Unlock()
	if store.reports != 0 || store.retires != 2 {
		t.Fatalf("not-ready observer reports=%d retires=%d", store.reports, store.retires)
	}
}

func TestTrafficRuntimeObserverKeepsBaselineAfterAmbiguousRegistration(t *testing.T) {
	store := &runtimeObserverTestStore{events: make(chan string, 20), registerErrors: []error{errors.New("committed response lost"), nil}}
	stop := startTrafficRuntimeObserver(t.Context(), store, "node", state.GatewayTrafficFeatures{RateCounterMode: "local", RetryCounterMode: "local"}, func() bool { return true }, discardLogger())
	t.Cleanup(stop)
	waitRuntimeObserverEvent(t, store.events, "report")
	stop()
	store.mu.Lock()
	defer store.mu.Unlock()
	if store.reads != 1 || store.registers != 2 || store.expected[0] != 7 || store.expected[1] != 7 || store.boots[0] != store.boots[1] {
		t.Fatalf("ambiguous recovery rebased ownership: reads=%d expected=%v boots=%v", store.reads, store.expected, store.boots)
	}
}

func TestTrafficRuntimeObserverStopsAfterOwnershipLoss(t *testing.T) {
	store := &runtimeObserverTestStore{events: make(chan string, 20), reportError: state.ErrGatewayTrafficEpochLost}
	stop := startTrafficRuntimeObserver(t.Context(), store, "node", state.GatewayTrafficFeatures{RateCounterMode: "local", RetryCounterMode: "local"}, func() bool { return true }, discardLogger())
	t.Cleanup(stop)
	waitRuntimeObserverEvent(t, store.events, "retire")
	stop()
	store.mu.Lock()
	defer store.mu.Unlock()
	if store.reads != 1 || store.registers != 1 || store.reports != 1 {
		t.Fatalf("lost generation reclaimed: reads=%d registers=%d reports=%d", store.reads, store.registers, store.reports)
	}
}
