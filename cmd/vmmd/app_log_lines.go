package main

import (
	"sync"
	"sync/atomic"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/fcvm/logbuf"
	"github.com/onebox-faas/faas/pkg/loglevel"
	"github.com/prometheus/client_golang/prometheus"
)

// appLogLineOverflow is the app_id every app past LogLineCounterMaxApps
// shares, so the series count stays bounded on a long-lived vmmd.
const appLogLineOverflow = "other"

// appLogLineCounter counts error and warn guest log lines per app as each
// line enters its instance ring (ADR-746). It is called under the ring
// mutex, so the hot path is a lock-free map hit plus an atomic add; the
// mutex is taken only the first time an app logs an error or warning, and
// never once the cap is reached.
type appLogLineCounter struct {
	lines    *prometheus.CounterVec
	apps     sync.Map // app id → *appLogLineSeries, at most LogLineCounterMaxApps
	overflow *appLogLineSeries
	full     atomic.Bool

	mu       sync.Mutex
	admitted int
}

type appLogLineSeries struct {
	errors, warns prometheus.Counter
}

func newAppLogLineCounter(reg prometheus.Registerer) *appLogLineCounter {
	c := &appLogLineCounter{lines: prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "vmmd_app_log_lines_total",
		Help: "Guest log lines classified as error or warn, per app (ADR-746). Level comes from the ring's structured-log parse, else the gregale logs --level heuristic over the first LogLevelScanBytes. Apps beyond LogLineCounterMaxApps per vmmd share app_id=\"other\".",
	}, []string{"app_id", "level"})}
	c.overflow = c.newSeries(appLogLineOverflow)
	if reg != nil {
		reg.MustRegister(c.lines)
	}
	return c
}

func (c *appLogLineCounter) newSeries(label string) *appLogLineSeries {
	return &appLogLineSeries{
		errors: c.lines.WithLabelValues(label, loglevel.Error),
		warns:  c.lines.WithLabelValues(label, loglevel.Warn),
	}
}

// Observe classifies one committed line and counts it when it is an error
// or a warning.
func (c *appLogLineCounter) Observe(appID string, line logbuf.Line) {
	level := line.Level
	if level == "" {
		text := line.Line
		if len(text) > api.LogLevelScanBytes {
			text = text[:api.LogLevelScanBytes]
		}
		level = loglevel.Detect(text)
	}
	switch level {
	case loglevel.Error:
		c.series(appID).errors.Inc()
	case loglevel.Warn:
		c.series(appID).warns.Inc()
	}
}

func (c *appLogLineCounter) series(appID string) *appLogLineSeries {
	if s, ok := c.apps.Load(appID); ok {
		return s.(*appLogLineSeries)
	}
	if c.full.Load() {
		return c.overflow
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if s, ok := c.apps.Load(appID); ok {
		return s.(*appLogLineSeries)
	}
	if c.admitted >= api.LogLineCounterMaxApps {
		c.full.Store(true)
		return c.overflow
	}
	c.admitted++
	s := c.newSeries(appID)
	c.apps.Store(appID, s)
	return s
}
