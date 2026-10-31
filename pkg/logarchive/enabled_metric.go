package logarchive

import (
	"errors"

	"github.com/prometheus/client_golang/prometheus"
)

// ReportEnabled exports <prefix>_log_archive_enabled: 1 when the daemon
// ships archive lines, 0 when its archive is off (no bucket, an empty or
// unreadable credential envelope, a client or shipper that failed to start).
// It is exported either way, so a node that came up without archiving shows
// in Prometheus instead of only in one startup log line.
func ReportEnabled(reg prometheus.Registerer, prefix string, enabled bool) {
	if reg == nil {
		return
	}
	gauge := prometheus.NewGauge(prometheus.GaugeOpts{
		Name: prefix + "_log_archive_enabled",
		Help: "1 when this daemon's log archive shipper runs, 0 when its archive is off (issue #562).",
	})
	if err := reg.Register(gauge); err != nil {
		var already prometheus.AlreadyRegisteredError
		if !errors.As(err, &already) {
			return
		}
		existing, ok := already.ExistingCollector.(prometheus.Gauge)
		if !ok {
			return
		}
		gauge = existing
	}
	value := 0.0
	if enabled {
		value = 1
	}
	gauge.Set(value)
}
