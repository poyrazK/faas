package main

// adr: 20261121202814 — only TLS (tcp/dns) vmmd targets get the hop; the
// local data socket and malformed targets stay on gRPC.

import "testing"

func TestForwardHopAddress(t *testing.T) {
	for target, want := range map[string]string{
		"tcp://10.20.0.3:50051":           "10.20.0.3:50051",
		"tcp://bhs-3.gregale.dev:50051":   "bhs-3.gregale.dev:50051",
		"dns:///bhs-3.gregale.dev:50051":  "bhs-3.gregale.dev:50051",
		"unix:///run/faas/vmmd-data.sock": "",
		"tcp://10.20.0.3:0":               "",
		"tcp://10.20.0.3":                 "",
		"":                                "",
	} {
		got, ok := forwardHopAddress(target)
		if (want == "") == ok || got != want {
			t.Errorf("forwardHopAddress(%q) = %q, %v; want %q", target, got, ok, want)
		}
	}
}
