package main

import (
	"testing"
	"time"
)

func TestParseConntrackNewOriginalTuple(t *testing.T) {
	tests := []struct {
		name, line, protocol, source, destination string
		sport, dport                              uint16
		want                                      bool
	}{
		{
			name:     "tcp with reply and timestamp",
			line:     "[1790500000.123456] [NEW] tcp 6 120 SYN_SENT src=10.100.0.5 dst=93.184.216.34 sport=42301 dport=443 [UNREPLIED] src=93.184.216.34 dst=10.100.0.5 sport=443 dport=42301",
			protocol: "tcp", source: "10.100.0.5", destination: "93.184.216.34", sport: 42301, dport: 443, want: true,
		},
		{
			name:     "udp IPv6",
			line:     "[1790500000.123456] [NEW] udp 17 30 src=fd00::5 dst=2001:4860:4860::8888 sport=53000 dport=53 src=2001:4860:4860::8888 dst=fd00::5 sport=53 dport=53000",
			protocol: "udp", source: "fd00::5", destination: "2001:4860:4860::8888", sport: 53000, dport: 53, want: true,
		},
		{name: "update is not a new connection", line: "[1790500000.123456] [UPDATE] tcp 6 1 src=10.100.0.5 dst=8.8.8.8 sport=1 dport=443"},
		{name: "malformed port", line: "[1790500000.123456] [NEW] tcp 6 1 src=10.100.0.5 dst=8.8.8.8 sport=broken dport=443"},
		{name: "unsupported protocol", line: "[1790500000.123456] [NEW] icmp 1 1 src=10.100.0.5 dst=8.8.8.8 sport=1 dport=2"},
		{name: "missing event timestamp", line: "[NEW] tcp 6 1 src=10.100.0.5 dst=8.8.8.8 sport=1 dport=443"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := parseConntrackNew(tt.line)
			if ok != tt.want {
				t.Fatalf("valid = %t, want %t: %#v", ok, tt.want, got)
			}
			if tt.want && (got.protocol != tt.protocol || got.sourceIP != tt.source || got.destinationIP != tt.destination || got.sourcePort != tt.sport || got.destinationPort != tt.dport) {
				t.Fatalf("original tuple = %#v", got)
			}
			if tt.want && !got.eventAt.Equal(time.Unix(1790500000, 123456000).UTC()) {
				t.Fatalf("event time = %s", got.eventAt)
			}
		})
	}
}
