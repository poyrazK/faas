package main

import (
	"fmt"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/fcvm/logbuf"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

func TestAppLogLineCounterClassifies(t *testing.T) {
	c := newAppLogLineCounter(prometheus.NewRegistry())
	for _, l := range []logbuf.Line{
		{Line: `{"level":"error"}`, Level: "error"}, // structured parse wins
		{Line: "[ERROR] payment declined"},          // heuristic fallback
		{Line: "level=warn msg=slow"},
		{Line: "[info] listening"},                                      // not counted
		{Line: "an error in prose"},                                     // unclassified
		{Line: "plain", Level: "info"},                                  // not counted
		{Line: strings.Repeat("x", api.LogLevelScanBytes) + " [error]"}, // past the scan window
	} {
		c.Observe("app-1", l)
	}
	if got := testutil.ToFloat64(c.lines.WithLabelValues("app-1", "error")); got != 2 {
		t.Errorf("errors = %v, want 2", got)
	}
	if got := testutil.ToFloat64(c.lines.WithLabelValues("app-1", "warn")); got != 1 {
		t.Errorf("warns = %v, want 1", got)
	}
}

func TestAppLogLineCounterCapsApps(t *testing.T) {
	c := newAppLogLineCounter(nil)
	for i := 0; i < api.LogLineCounterMaxApps+3; i++ {
		c.Observe(fmt.Sprintf("app-%d", i), logbuf.Line{Line: "[error] x"})
	}
	if got := testutil.ToFloat64(c.lines.WithLabelValues(appLogLineOverflow, "error")); got != 3 {
		t.Errorf("overflow errors = %v, want 3", got)
	}
	n := 0
	c.apps.Range(func(any, any) bool { n++; return true })
	if n != api.LogLineCounterMaxApps {
		t.Errorf("tracked apps = %d, want the cap %d", n, api.LogLineCounterMaxApps)
	}
	// An admitted app keeps its own series after the cap is reached.
	c.Observe("app-0", logbuf.Line{Line: "[warn] y"})
	if got := testutil.ToFloat64(c.lines.WithLabelValues("app-0", "warn")); got != 1 {
		t.Errorf("admitted app warns = %v, want 1", got)
	}
}
