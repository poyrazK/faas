package edgedns

import "github.com/prometheus/client_golang/prometheus"

// ErrorsForTest exposes one op's error counter to the external tests.
func (m *Metrics) ErrorsForTest(op string) prometheus.Counter {
	return m.errors.WithLabelValues(op)
}
