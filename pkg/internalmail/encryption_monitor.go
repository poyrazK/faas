package internalmail

import (
	"context"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/prometheus/client_golang/prometheus"
)

type encryptionStatusReader interface {
	encryptionStatus(context.Context, *KeyringCodec) (EncryptionStatus, error)
}

type EncryptionMonitor struct {
	source  encryptionStatusReader
	codec   *KeyringCodec
	enabled func() bool
	desc    map[string]*prometheus.Desc
}

func NewEncryptionMonitor(source encryptionStatusReader, codec *KeyringCodec, enabled func() bool) *EncryptionMonitor {
	m := &EncryptionMonitor{source: source, codec: codec, enabled: enabled, desc: map[string]*prometheus.Desc{}}
	for _, d := range []struct{ name, help string }{
		{"enabled", "Internal stored-mail encryption rotation management gate."},
		{"stats_up", "Whether encryption dependency aggregates were read successfully."},
		{"missing_required_keys", "Required identities absent from configuration or durably retired."},
		{"old_payloads", "Retained payloads encrypted under a nonactive identity."},
		{"old_signing_keys", "Stored DKIM private key copies encrypted under a nonactive identity."},
		{"old_fingerprints", "Replay fingerprints requiring a nonactive identity."},
		{"pruned_old_fingerprints", "Pruned payload replay fingerprints requiring a nonactive identity; these block retirement."},
	} {
		m.desc[d.name] = prometheus.NewDesc("maild_mail_encryption_"+d.name, d.help, nil, nil)
	}
	return m
}

func (m *EncryptionMonitor) Describe(ch chan<- *prometheus.Desc) {
	for _, desc := range m.desc {
		ch <- desc
	}
}

func (m *EncryptionMonitor) Collect(ch chan<- prometheus.Metric) {
	emit := func(name string, value float64) {
		ch <- prometheus.MustNewConstMetric(m.desc[name], prometheus.GaugeValue, value)
	}
	on := m.enabled != nil && m.enabled()
	emit("enabled", boolMetric(on))
	if !on {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), api.InternalMailMonitoringTimeout)
	defer cancel()
	status, err := m.source.encryptionStatus(ctx, m.codec)
	emit("stats_up", boolMetric(err == nil))
	if err != nil {
		return
	}
	var missing, payloads, signing, fingerprints, pruned int64
	for _, key := range status.Keys {
		if key.required() && (!key.Configured || key.RetiredAt != nil) {
			missing++
		}
		if key.Active {
			continue
		}
		payloads += key.Payloads
		signing += key.DomainKeys + key.DKIMKeys
		fingerprints += key.Fingerprints
		pruned += key.PrunedFingerprints
	}
	emit("missing_required_keys", float64(missing))
	emit("old_payloads", float64(payloads))
	emit("old_signing_keys", float64(signing))
	emit("old_fingerprints", float64(fingerprints))
	emit("pruned_old_fingerprints", float64(pruned))
}
