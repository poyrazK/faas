package meter_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/meter"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/wire"
)

// runLoopBrief runs a Loop with sub-second intervals until each wired
// timer has at least one tick, then cancels and waits for clean drain.
// Returns the Loop and the ops handle so the test can scrape the
// registry and (separately) inspect the per-tick last-fire map.
//
// fixtureStore can be nil — the sample / quota loops may return errors
// but tick bodies still run, so Observe is called.
func runLoopBrief(t *testing.T, store state.Store, dunning *meter.Dunning) (*meter.Loop, *wire.OpsMetrics) {
	t.Helper()
	ops := wire.NewOpsMetrics("meter_test_observe")
	cfg := &meter.Config{}
	cfg.Defaults()
	cfg.SampleInterval = 20 * time.Millisecond
	cfg.QuotaInterval = 20 * time.Millisecond
	cfg.StripeInterval = 20 * time.Millisecond
	cfg.DunningInterval = 20 * time.Millisecond

	loop := meter.NewLoop(
		store,
		&fakeParker{},
		nil, // StripePusher — nil; the pusher returns nil, error "pusher not configured"
		&fakeNotifier{},
		dunning,
		func() time.Time { return time.Date(2026, 7, 21, 12, 0, 0, 0, time.UTC) },
		discardLog(),
		cfg,
		ops,
	)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- loop.Run(ctx) }()
	time.Sleep(150 * time.Millisecond)
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("loop returned %v, want nil on cancel", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("loop did not return within 3s of cancel")
	}
	return loop, ops
}

// scrapeBody renders the registry as text. Mirrors the canonical
// pkg/wire/metrics_test.go:14-54 — scrape after a real Observe cycle
// and substring-assert on the Prometheus text format.
func scrapeBody(t *testing.T, ops *wire.OpsMetrics) string {
	t.Helper()
	mfs, err := ops.Registry().Gather()
	if err != nil {
		t.Fatalf("registry.Gather: %v", err)
	}
	var out []string
	for _, mf := range mfs {
		for _, metric := range mf.GetMetric() {
			var labels []string
			for _, lp := range metric.GetLabel() {
				labels = append(labels, lp.GetName()+"="+lp.GetValue())
			}
			switch {
			case strings.HasSuffix(mf.GetName(), "_total"):
				out = append(out, mf.GetName()+"{"+strings.Join(labels, ",")+"} "+
					formatCounter(metric.GetCounter().GetValue()))
			case mf.GetType().String() == "HISTOGRAM":
				h := metric.GetHistogram()
				if h == nil {
					continue
				}
				out = append(out, mf.GetName()+"_count{"+strings.Join(labels, ",")+"} "+
					formatCounter(float64(h.GetSampleCount())))
			}
		}
	}
	return strings.Join(out, "\n")
}

// formatCounter renders a Prometheus counter value as the promhttp text
// encoder would — integer-valued counts without decimals. Counter rows
// are integer-formatted by Prometheus; histogram counts are too.
func formatCounter(v float64) string {
	if v == float64(int64(v)) {
		return intStr(int64(v))
	}
	return floatStr(v)
}

func intStr(n int64) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	out := ""
	for n > 0 {
		out = string(rune('0'+n%10)) + out
		n /= 10
	}
	if neg {
		out = "-" + out
	}
	return out
}

func floatStr(v float64) string {
	neg := v < 0
	if neg {
		v = -v
	}
	intPart := int64(v)
	frac := v - float64(intPart)
	out := intStr(intPart)
	if frac > 0 {
		out += "."
		for i := 0; i < 3; i++ {
			frac *= 10
			d := int64(frac)
			out += string(rune('0' + d))
			frac -= float64(d)
		}
		out = strings.TrimRight(out, "0")
		out = strings.TrimRight(out, ".")
	}
	if neg {
		out = "-" + out
	}
	return out
}

// TestLoop_ObserveSampleAndStripe — the two ops that share the
// runTicks path. Asserts both ops surface in the registry with at
// least one *_total{op=...} line. Fails closed if Observe isn't wired.
func TestLoop_ObserveSampleAndStripe(t *testing.T) {
	t.Parallel()
	store := state.NewMemStore()
	// Seed at least one app so SampleAndRoll has work to do (else it
	// returns []RolledRow with no error; loop still ticks).
	acct, err := store.CreateAccount(context.Background(), "u@example.com", api.PlanHobby)
	if err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}
	if _, err := store.CreateApp(context.Background(), state.App{
		AccountID: acct.ID, Slug: "test-app", Type: state.AppTypeApp,
	}); err != nil {
		t.Fatalf("CreateApp: %v", err)
	}

	_, ops := runLoopBrief(t, store, nil)
	body := scrapeBody(t, ops)

	// Either ok or err counts as a wired Observe. We don't pin which
	// here; the metric value presence is what matters. scrapeBody
	// emits `op=sample` (no quotes — see scrapeBody implementation) so
	// we substring-match on the unquoted form.
	hasSample := strings.Contains(body, `op=sample,`) || strings.Contains(body, `,op=sample `) ||
		strings.Contains(body, `op=sample}`)
	hasStripe := strings.Contains(body, `op=stripe,`) || strings.Contains(body, `,op=stripe `) ||
		strings.Contains(body, `op=stripe}`)
	if !hasSample {
		t.Errorf("missing sample op in /metrics registry:\n%s", body)
	}
	if !hasStripe {
		t.Errorf("missing stripe op in /metrics registry:\n%s", body)
	}
}

// TestLoop_ObserveQuotaHistogram — quota's runQuotaOnce always passes
// nil to Observe from runQuotaTicks (per pkg/meter/loop.go — quota has
// no aggregate err to surface). The histogram _count line is the
// stable assertion across both ok and empty-account setups.
func TestLoop_ObserveQuotaHistogram(t *testing.T) {
	t.Parallel()
	store := state.NewMemStore()
	if _, err := store.CreateAccount(context.Background(), "u@example.com", api.PlanHobby); err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}
	_, ops := runLoopBrief(t, store, nil)
	body := scrapeBody(t, ops)
	hasQuota := strings.Contains(body, `op=quota,`) || strings.Contains(body, `,op=quota `) ||
		strings.Contains(body, `op=quota}`)
	if !hasQuota {
		t.Errorf("missing quota op in /metrics registry:\n%s", body)
	}
}
