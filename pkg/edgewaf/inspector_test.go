package edgewaf

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/gateway"
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

// fakeClock lets budget tests move time without sleeping.
type fakeClock struct{ t time.Time }

func (c *fakeClock) now() time.Time          { return c.t }
func (c *fakeClock) advance(d time.Duration) { c.t = c.t.Add(d) }
func newClockedInspector(obs Observer) (*Inspector, *fakeClock) {
	i := New(obs, nil)
	c := &fakeClock{t: time.Date(2026, 10, 10, 0, 0, 0, 0, time.UTC)}
	i.now = c.now
	return i, c
}

func TestSubmitSamplesOutAboveAppBudget(t *testing.T) {
	obs := &recordingObserver{}
	i, _ := newClockedInspector(obs)
	s := sample(http.MethodPost, "/", nil, strings.Repeat("x", api.EdgeWAFDefaultInspectBodyBytes))
	// No workers run, so admitted samples stay queued. An 8 KiB sample is
	// estimated at ~207 ms, so one 2000 ms burst admits 10 of them.
	for range 15 {
		i.Submit(s)
	}
	if got := len(i.queue); got != 10 {
		t.Errorf("queued %d 8 KiB samples, want 10", got)
	}
	if got := obs.count(OutcomeSampledOut); got != 5 {
		t.Errorf("sampled_out = %d, want 5", got)
	}
	other := s
	other.AppID = "app-2"
	i.Submit(other)
	if got := obs.count(OutcomeSampledOut); got != 5 {
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

func TestBudgetAdmitsByEstimatedWorkerTime(t *testing.T) {
	for _, tc := range []struct {
		name          string
		paranoiaLevel int
		bodySize      int
		want          int
	}{
		// 2000 ms burst / per-sample estimate, rounded up because a sample
		// is admitted while any balance remains.
		{name: "headers only", paranoiaLevel: 1, bodySize: 0, want: 1000},
		{name: "8 KiB PL1", paranoiaLevel: 1, bodySize: 8192, want: 10},
		{name: "8 KiB PL2", paranoiaLevel: 2, bodySize: 8192, want: 5},
		{name: "64 KiB PL1", paranoiaLevel: 1, bodySize: 65536, want: 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			i, _ := newClockedInspector(&recordingObserver{})
			est := estimateMs(tc.paranoiaLevel, tc.bodySize)
			got := 0
			for got < 10000 && i.admit("app-1", est) {
				got++
			}
			if got != tc.want {
				t.Errorf("admitted %d samples at %.1f ms each, want %d", got, est, tc.want)
			}
		})
	}
}

func TestBudgetSettlesToMeasuredTime(t *testing.T) {
	i, clock := newClockedInspector(&recordingObserver{})
	est := estimateMs(1, 0)
	if !i.admit("app-1", est) {
		t.Fatal("first sample not admitted")
	}
	// The sample took far longer than estimated: the overrun is charged, so
	// the app waits for its balance to refill.
	i.settle(job{sample: gatewaySample("app-1"), estimateMs: est}, 3*time.Second)
	if i.admit("app-1", est) {
		t.Fatal("admitted while the app is in debt")
	}
	// Debt is capped at one burst: from -2000 ms at 400 ms/s the balance is
	// positive again after just over 5 s.
	clock.advance(5*time.Second + 10*time.Millisecond)
	if !i.admit("app-1", est) {
		t.Fatal("not admitted after the debt refilled")
	}

	// A sample cheaper than estimated refunds the difference.
	j, _ := newClockedInspector(&recordingObserver{})
	big := estimateMs(1, 65536)
	j.admit("app-1", big)
	j.settle(job{sample: gatewaySample("app-1"), estimateMs: big}, 0)
	if b := j.budgets["app-1"].balance; b != api.EdgeWAFWorkerMsPerAppBurst {
		t.Errorf("balance after a free sample = %.1f, want the full burst back", b)
	}
}

func TestDroppedSampleIsRefunded(t *testing.T) {
	obs := &recordingObserver{}
	i, _ := newClockedInspector(obs)
	s := sample(http.MethodGet, "/", nil, "")
	for n := 0; len(i.queue) < cap(i.queue); n++ {
		s.AppID = "filler-" + strconv.Itoa(n)
		i.Submit(s)
	}
	s.AppID = "app-1"
	i.Submit(s)
	if got := obs.count(OutcomeDropped); got != 1 {
		t.Fatalf("dropped = %d, want 1", got)
	}
	if b := i.budgets["app-1"].balance; b != api.EdgeWAFWorkerMsPerAppBurst {
		t.Errorf("balance after a dropped sample = %.1f, want the full burst", b)
	}
}

func gatewaySample(appID string) gateway.WAFSample {
	s := sample(http.MethodGet, "/", nil, "")
	s.AppID = appID
	return s
}
