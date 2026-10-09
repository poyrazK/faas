// adr: 733
package imaged

import (
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

// crashCaptureMetrics are imaged's ADR-733 encryption-at-rest signals. All
// methods are nil-safe: a loop built without an ops registry records nothing.
type crashCaptureMetrics struct {
	unencryptedOldest prometheus.Gauge
	keyMissing        prometheus.Gauge
	ops               *prometheus.CounterVec
}

func newCrashCaptureMetrics(reg prometheus.Registerer) *crashCaptureMetrics {
	m := &crashCaptureMetrics{
		unencryptedOldest: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "imaged_crash_capture_unencrypted_oldest_age_seconds",
			Help: "Age of the oldest ready crash capture whose plaintext is not yet encrypted; 0 when none. ADR-733 expects at most one 5 s pass.",
		}),
		keyMissing: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "imaged_crash_capture_encryption_key_missing",
			Help: "1 when crash captures wait for encryption but imaged has no host age identity (FAAS_HOST_AGE_IDENTITY_PATH), else 0.",
		}),
		ops: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "imaged_crash_capture_ops_total",
			Help: "Crash capture file operations by op (encrypt, purge, stage, expire) and result (ok, error).",
		}, []string{"op", "result"}),
	}
	reg.MustRegister(m.unencryptedOldest, m.keyMissing, m.ops)
	for _, op := range []string{"encrypt", "purge", "stage", "expire"} {
		for _, result := range []string{"ok", "error"} {
			m.ops.WithLabelValues(op, result)
		}
	}
	return m
}

// pending records the unencrypted backlog: oldest is the capture time of the
// oldest unencrypted capture (zero when none), keyMissing whether imaged
// lacks the key to encrypt it.
func (m *crashCaptureMetrics) pending(now, oldest time.Time, keyMissing bool) {
	if m == nil {
		return
	}
	age := 0.0
	if !oldest.IsZero() {
		age = max(0, now.Sub(oldest).Seconds())
	}
	m.unencryptedOldest.Set(age)
	missing := 0.0
	if keyMissing {
		missing = 1
	}
	m.keyMissing.Set(missing)
}

func (m *crashCaptureMetrics) op(op string, err error) {
	if m == nil {
		return
	}
	result := "ok"
	if err != nil {
		result = "error"
	}
	m.ops.WithLabelValues(op, result).Inc()
}
