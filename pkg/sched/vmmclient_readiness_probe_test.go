package sched

import "testing"

func TestAppSpecToProtoCarriesReadinessProbe(t *testing.T) {
	const raw = `{"path":"/readyz","period_s":7}`
	got := (AppSpec{ReadinessProbeJSON: raw}).toProto().GetReadinessProbeJson()
	if got != raw {
		t.Fatalf("readiness probe JSON = %q, want %q", got, raw)
	}
}
