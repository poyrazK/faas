package edgewaf

import (
	"context"
	"net/http"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/gateway"
)

type recordingEvents struct {
	mu     sync.Mutex
	allow  bool
	events []gateway.EdgeRuleEvent
}

func (r *recordingEvents) SampleEdgeRuleDetection(string) bool { return r.allow }
func (r *recordingEvents) RecordEdgeRuleEvent(ev gateway.EdgeRuleEvent) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, ev)
}

func (r *recordingEvents) snapshot() []gateway.EdgeRuleEvent {
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.Clone(r.events)
}

// ADR-831 amendment 5: an off-path detection becomes a "detected" security
// event within the recorder's budget; clean samples never do.
func TestOffPathDetectionsBecomeEvents(t *testing.T) {
	for _, allow := range []bool{true, false} {
		obs := &recordingObserver{}
		events := &recordingEvents{allow: allow}
		i := New(obs, nil).WithEventRecorder(events)
		shared, _ := sharedInspector(t).engine(1)
		i.engines[1].once.Do(func() { i.engines[1].waf = shared })
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan struct{})
		go func() { i.Run(ctx); close(done) }()
		attack := sample(http.MethodGet, "/api/items?q=1%27%20OR%201%3D1--", nil, "")
		attack.RuleID, attack.RequestID = "rule-1", "req-1"
		i.Submit(attack)
		i.Submit(sample(http.MethodGet, "/api/items?page=2", nil, ""))
		for deadline := time.Now().Add(10 * time.Second); obs.count(OutcomeDetected)+obs.count(OutcomeClean) < 2 && time.Now().Before(deadline); {
			time.Sleep(5 * time.Millisecond)
		}
		cancel()
		<-done

		got := events.snapshot()
		if !allow {
			if len(got) != 0 {
				t.Fatalf("events over budget = %+v, want none", got)
			}
			continue
		}
		if len(got) != 1 {
			t.Fatalf("events = %+v, want one detection", got)
		}
		ev := got[0]
		if ev.Outcome != gateway.EdgeRuleEventDetected || ev.RuleID != "rule-1" || ev.RequestID != "req-1" ||
			ev.Path != "/api/items" || !slices.Contains(ev.WAFRuleIDs, 942100) || !slices.Contains(ev.WAFCategories, "sqli") || ev.WAFScore < 5 {
			t.Errorf("event = %+v", ev)
		}
	}
}
