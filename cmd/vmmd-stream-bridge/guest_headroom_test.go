package main

// adr: 20261026120000 — with FAAS_BRIDGE_GUEST_HEADROOM=1 the bridge keeps
// one idle guest connection per accepted stream and dials the guest briefly.

import (
	"testing"
	"time"
)

func TestGuestHeadroomSizing(t *testing.T) {
	cases := []struct {
		name     string
		env      string
		wantIdle int
		wantDial time.Duration
	}{
		{name: "unset keeps the old pool", env: "", wantIdle: guestTransportMaxIdleConnsPerHost, wantDial: dialTimeout},
		{name: "headroom sizes to the stream cap", env: "1", wantIdle: h2cMaxConcurrentStreams, wantDial: guestHeadroomDialTimeout},
		{name: "any other value keeps the old pool", env: "true", wantIdle: guestTransportMaxIdleConnsPerHost, wantDial: dialTimeout},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("FAAS_BRIDGE_GUEST_HEADROOM", tc.env)
			if got := guestIdleConnsPerHost(); got != tc.wantIdle {
				t.Errorf("idle conns = %d, want %d", got, tc.wantIdle)
			}
			if got := guestDialTimeout(); got != tc.wantDial {
				t.Errorf("dial timeout = %v, want %v", got, tc.wantDial)
			}
			tr := newGuestH1Transport("10.0.0.2", 8080)
			if tr.MaxIdleConnsPerHost != tc.wantIdle || tr.MaxIdleConns != tc.wantIdle {
				t.Errorf("transport idle pool = %d/%d, want %d", tr.MaxIdleConns, tr.MaxIdleConnsPerHost, tc.wantIdle)
			}
		})
	}
}
