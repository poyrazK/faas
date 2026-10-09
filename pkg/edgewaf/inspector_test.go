package edgewaf

import (
	"context"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

type recordingObserver struct {
	mu         sync.Mutex
	outcomes   map[string]int
	categories map[string]int
	rules      map[int]int
}

func (o *recordingObserver) ObserveWAFInspection(_, outcome string, _ float64) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.outcomes == nil {
		o.outcomes = map[string]int{}
	}
	o.outcomes[outcome]++
}

func (o *recordingObserver) ObserveWAFDetection(_, category string) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.categories == nil {
		o.categories = map[string]int{}
	}
	o.categories[category]++
}

func (o *recordingObserver) ObserveWAFRuleMatch(_ string, ruleID int) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.rules == nil {
		o.rules = map[int]int{}
	}
	o.rules[ruleID]++
}

func (o *recordingObserver) count(outcome string) int {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.outcomes[outcome]
}

func TestSubmitSamplesOutAboveAppBudget(t *testing.T) {
	obs := &recordingObserver{}
	i := New(obs, nil)
	s := sample(http.MethodGet, "/", nil, "")
	// No workers run, so every sample within the burst stays queued.
	for range api.EdgeWAFInspectionsPerAppBurst + 5 {
		i.Submit(s)
	}
	if got := obs.count(OutcomeSampledOut); got < 5 {
		t.Errorf("sampled_out = %d, want >= 5", got)
	}
	if got := len(i.queue); got > api.EdgeWAFInspectionsPerAppBurst {
		t.Errorf("queued %d samples, want <= burst %d", got, api.EdgeWAFInspectionsPerAppBurst)
	}
	other := s
	other.AppID = "app-2"
	i.Submit(other)
	if got := obs.count(OutcomeSampledOut); got > 5+1 {
		t.Errorf("another app's sample was charged to app-1's budget (sampled_out=%d)", got)
	}
}

func TestSubmitDropsWhenQueueFull(t *testing.T) {
	obs := &recordingObserver{}
	i := New(obs, nil)
	s := sample(http.MethodGet, "/", nil, "")
	for n := 0; len(i.queue) < cap(i.queue); n++ {
		s.AppID = "app-" + string(rune('a'+n%26)) + string(rune('a'+n/26))
		i.Submit(s)
	}
	s.AppID = "fresh-app"
	i.Submit(s)
	if got := obs.count(OutcomeDropped); got != 1 {
		t.Errorf("dropped = %d, want 1", got)
	}
}

func TestRunReportsDetection(t *testing.T) {
	obs := &recordingObserver{}
	i := New(obs, nil)
	shared, _ := sharedInspector(t).engine(1)
	i.engines[1].once.Do(func() { i.engines[1].waf = shared })
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { i.Run(ctx); close(done) }()
	i.Submit(sample(http.MethodGet, "/api/items?q=1%27%20OR%201%3D1--", nil, ""))
	i.Submit(sample(http.MethodGet, "/api/items?page=2", nil, ""))
	deadline := time.Now().Add(10 * time.Second)
	for obs.count(OutcomeDetected)+obs.count(OutcomeClean) < 2 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	cancel()
	<-done
	if obs.count(OutcomeDetected) != 1 || obs.count(OutcomeClean) != 1 {
		t.Fatalf("outcomes = %v, want 1 detected and 1 clean", obs.outcomes)
	}
	if obs.categories["sqli"] != 1 {
		t.Errorf("categories = %v, want sqli", obs.categories)
	}
	if obs.rules[942100] != 1 {
		t.Errorf("rule matches = %v, want 942100", obs.rules)
	}
}

func TestSubmitChargesBudgetByBodySize(t *testing.T) {
	for _, tc := range []struct {
		name     string
		bodySize int
	}{
		{name: "no body", bodySize: 0},
		{name: "8 KiB body", bodySize: api.EdgeWAFDefaultInspectBodyBytes},
		{name: "64 KiB body", bodySize: api.MaxEdgeWAFInspectBodyBytes},
	} {
		t.Run(tc.name, func(t *testing.T) {
			i := New(&recordingObserver{}, nil)
			s := sample(http.MethodPost, "/", nil, strings.Repeat("x", tc.bodySize))
			for range api.EdgeWAFInspectionsPerAppBurst {
				i.Submit(s)
			}
			want := api.EdgeWAFInspectionsPerAppBurst / budgetTokens(tc.bodySize)
			if got := len(i.queue); got != want {
				t.Errorf("queued %d samples within one burst, want %d", got, want)
			}
		})
	}
}
