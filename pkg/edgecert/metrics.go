package edgecert

import (
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

// Metrics are the issuer's series (ADR-20261103163954 §3). A nil *Metrics
// records nothing.
type Metrics struct {
	orders  *prometheus.CounterVec
	expiry  *prometheus.GaugeVec
	account *prometheus.GaugeVec
}

// NewMetrics registers the series with reg.
func NewMetrics(reg prometheus.Registerer) *Metrics {
	m := &Metrics{
		orders: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "edge_certificate_orders_total",
			Help: "Certificate orders edgednsd placed for the apps domains, by CA and result (issued, failed); a failed order is retried after a growing pause (ADR-20261103163954 §3).",
		}, []string{"ca", "result"}),
		expiry: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "edge_certificate_stored_expiry_timestamp_seconds",
			Help: "Expiry, in Unix seconds, of the newest apps-domain certificate edgednsd stored, by CA (ADR-20261103163954 §3).",
		}, []string{"ca"}),
		account: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "edge_certificate_acme_account_info",
			Help: "1 for the ACME account edgednsd orders with, by CA and account URI (ADR-20261103163954 §3).",
		}, []string{"ca", "account_uri"}),
	}
	reg.MustRegister(m.orders, m.expiry, m.account)
	return m
}

func (m *Metrics) ordered(ca, result string) {
	if m != nil {
		m.orders.WithLabelValues(ca, result).Inc()
	}
}

func (m *Metrics) stored(ca string, notAfter time.Time) {
	if m != nil {
		m.expiry.WithLabelValues(ca).Set(float64(notAfter.Unix()))
	}
}

func (m *Metrics) accountURI(ca, uri string) {
	if m != nil && uri != "" {
		m.account.Reset()
		m.account.WithLabelValues(ca, uri).Set(1)
	}
}
