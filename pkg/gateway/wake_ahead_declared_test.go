package gateway

// adr: 1137

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestWakeAheadLearnerDeclaredPrior(t *testing.T) {
	tests := []struct {
		name     string
		wakes    int
		calls    map[string]bool
		declared []string
		want     []string
	}{
		{"declared targets stand in before evidence", 0, nil, []string{"auth", "billing"}, []string{"auth", "billing"}},
		{"self, empty and repeated names are dropped", 3, nil, []string{"auth", "", "api", "auth", "billing"}, []string{"auth", "billing"}},
		{"declaration order and the target cap", 0, nil, []string{"a", "b", "c", "d", "e"}, []string{"a", "b", "c"}},
		{"no declared targets and no evidence", 0, nil, nil, nil},
		{"evidence demotes a declared target the caller never calls", api.ServiceWakeAheadMinWakes, nil, []string{"auth"}, nil},
		{"evidence keeps a declared target the caller calls cold", api.ServiceWakeAheadMinWakes, map[string]bool{"auth": true}, []string{"auth"}, []string{"auth"}},
		{"evidence demotes a declared target that is usually warm", api.ServiceWakeAheadMinWakes, map[string]bool{"auth": false}, []string{"auth"}, nil},
		{"measured targets need no declaration", api.ServiceWakeAheadMinWakes, map[string]bool{"billing": true}, []string{"auth"}, []string{"billing"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			l, clock, _ := newTestLearner()
			train(l, clock, "api", tt.wakes, tt.calls)
			l.ObserveWake("api")
			if got := l.PredictWithDeclared("api", tt.declared); !slices.Equal(got, tt.want) {
				t.Fatalf("PredictWithDeclared = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestWakeAheadLearnerPredictIgnoresDeclarations(t *testing.T) {
	l, _, _ := newTestLearner()
	l.ObserveWake("api")
	if got := l.Predict("api"); got != nil {
		t.Fatalf("Predict = %v, want measured edges only", got)
	}
}

func TestWakeAheadWakesDeclaredTargetsBeforeEvidence(t *testing.T) {
	f := newWakeAheadFixture(t)
	reads := 0
	f.h.wakeAhead.cfg.Declared = func(_ context.Context, appID string) ([]string, error) {
		reads++
		if appID == "web" {
			return []string{"billing"}, nil
		}
		return nil, nil
	}
	// web has never been seen by this gateway: no evidence, so its declared
	// dependency is woken at the first cold wake.
	f.h.noteColdWake(context.Background(), "web")
	if target, depth := f.expectWake(t); target != "billing" || depth != 1 {
		t.Fatalf("woke %s at depth %d, want billing at depth 1", target, depth)
	}
	if got := f.h.wakeAhead.declaredTargets(context.Background(), "web"); !slices.Equal(got, []string{"billing"}) || reads != 1 {
		t.Fatalf("declared = %v after %d reads, want billing from the cache", got, reads)
	}
}

func TestWakeAheadDeclaredGuards(t *testing.T) {
	tests := []struct {
		name     string
		enabled  bool
		declared func(context.Context, string) ([]string, error)
	}{
		{"caller did not opt in", false, func(context.Context, string) ([]string, error) { return []string{"billing"}, nil }},
		{"failed read declares nothing", true, func(context.Context, string) ([]string, error) { return []string{"billing"}, errors.New("db down") }},
		{"nothing declared", true, func(context.Context, string) ([]string, error) { return nil, nil }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newWakeAheadFixture(t)
			f.enabled = tt.enabled
			f.h.wakeAhead.cfg.Declared = tt.declared
			f.h.noteColdWake(context.Background(), "web")
			f.expectNoWake(t)
		})
	}
}

func TestWakeAheadDeclaredNotReadWithoutOptIn(t *testing.T) {
	f := newWakeAheadFixture(t)
	f.enabled = false
	reads := 0
	f.h.wakeAhead.cfg.Declared = func(context.Context, string) ([]string, error) { reads++; return []string{"billing"}, nil }
	f.h.runWakeAhead(context.Background(), "web", 1)
	if reads != 0 {
		t.Fatalf("declared reads = %d, want none for a caller that did not opt in", reads)
	}
}
