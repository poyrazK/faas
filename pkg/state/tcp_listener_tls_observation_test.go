package state

import (
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestTCPListenerTLSObservationStatus(t *testing.T) {
	now := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	listener := TCPListener{ID: "listener", Enabled: true, TLSMode: api.TCPListenerTLSTerminate, TLSHostname: "echo.example", UpdatedAt: now.Add(-time.Minute)}
	observation := TCPListenerTLSObservation{ListenerID: listener.ID, EdgeID: "edge-one", Hostname: listener.TLSHostname, IntentUpdatedAt: listener.UpdatedAt, ObservedAt: now.Add(-time.Second), Ready: true, NotAfter: now.Add(time.Hour)}
	for _, scenario := range []struct {
		name   string
		change func(*TCPListenerTLSObservation, *TCPListener)
		want   string
	}{
		{"ready", func(*TCPListenerTLSObservation, *TCPListener) {}, "ready"},
		{"provider-unavailable", func(o *TCPListenerTLSObservation, _ *TCPListener) { o.Ready = false; o.NotAfter = time.Time{} }, "not_ready"},
		{"expired-since-observation", func(o *TCPListenerTLSObservation, _ *TCPListener) { o.NotAfter = now }, "not_ready"},
		{"stale-at-boundary", func(o *TCPListenerTLSObservation, _ *TCPListener) {
			o.ObservedAt = now.Add(-api.TCPListenerTLSObservationMaxAge)
		}, "unknown"},
		{"future-clock", func(o *TCPListenerTLSObservation, _ *TCPListener) { o.ObservedAt = now.Add(time.Second) }, "unknown"},
		{"policy-updated", func(_ *TCPListenerTLSObservation, l *TCPListener) { l.UpdatedAt = now }, "unknown"},
		{"policy-hostname-changed", func(_ *TCPListenerTLSObservation, l *TCPListener) { l.TLSHostname = "other.example" }, "unknown"},
		{"foreign-listener", func(o *TCPListenerTLSObservation, _ *TCPListener) { o.ListenerID = "other" }, "unknown"},
		{"disabled", func(_ *TCPListenerTLSObservation, l *TCPListener) { l.Enabled = false }, "unknown"},
		{"passthrough", func(_ *TCPListenerTLSObservation, l *TCPListener) { l.TLSMode = api.TCPListenerTLSPassthrough }, "unknown"},
		{"invalid-evidence", func(o *TCPListenerTLSObservation, _ *TCPListener) { o.EdgeID = "" }, "unknown"},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			o, l := observation, listener
			scenario.change(&o, &l)
			if got := o.Status(l, now); got != scenario.want {
				t.Fatalf("status=%q want=%q", got, scenario.want)
			}
		})
	}
	for _, edge := range []string{"", " edge", "edge\nname", "edge\xff", strings.Repeat("e", api.TCPListenerTLSObservationEdgeIDMaxBytes+1)} {
		invalid := observation
		invalid.EdgeID = edge
		if invalid.Validate() == nil {
			t.Errorf("accepted edge identity %q", edge)
		}
	}
}
