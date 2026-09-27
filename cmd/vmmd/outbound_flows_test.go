package main

import (
	"io"
	"log/slog"
	"net/netip"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/fcvm"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestGuestFlowSourceUsesConfiguredBridgeAndExcludesHostAddresses(t *testing.T) {
	bridge := netip.MustParsePrefix("10.101.0.0/16")
	for _, source := range []string{"10.101.0.2", "10.101.40.5"} {
		if !guestFlowSource(bridge, source) {
			t.Fatalf("guest address %s was excluded", source)
		}
	}
	for _, source := range []string{"10.101.0.0", "10.101.0.1", "10.100.0.2", "127.0.0.1", "bad"} {
		if guestFlowSource(bridge, source) {
			t.Fatalf("host or unrelated address %s was included", source)
		}
	}
}

func TestParseConntrackNewOriginalTuple(t *testing.T) {
	tests := []struct {
		name, line, protocol, source, destination, replyDestination string
		sport, dport, replyPort                                     uint16
		want                                                        bool
	}{
		{
			name:     "tcp with reply and timestamp",
			line:     "[1790500000.123456] [NEW] tcp 6 120 SYN_SENT src=10.100.0.5 dst=93.184.216.34 sport=42301 dport=443 [UNREPLIED] src=93.184.216.34 dst=192.0.2.10 sport=443 dport=50888",
			protocol: "tcp", source: "10.100.0.5", destination: "93.184.216.34", sport: 42301, dport: 443, replyDestination: "192.0.2.10", replyPort: 50888, want: true,
		},
		{
			name:     "udp IPv6",
			line:     "[1790500000.123456] [NEW] udp 17 30 src=fd00::5 dst=2001:4860:4860::8888 sport=53000 dport=53 src=2001:4860:4860::8888 dst=fd00::5 sport=53 dport=53000",
			protocol: "udp", source: "fd00::5", destination: "2001:4860:4860::8888", sport: 53000, dport: 53, replyDestination: "fd00::5", replyPort: 53000, want: true,
		},
		{name: "original only", line: "[1790500000.123456] [NEW] udp 17 30 src=10.100.0.5 dst=8.8.8.8 sport=53000 dport=53", protocol: "udp", source: "10.100.0.5", destination: "8.8.8.8", sport: 53000, dport: 53, want: true},
		{name: "partial reply", line: "[1790500000.123456] [NEW] tcp 6 1 src=10.100.0.5 dst=8.8.8.8 sport=1 dport=443 src=8.8.8.8 dst=192.0.2.10"},
		{name: "bad reply port", line: "[1790500000.123456] [NEW] tcp 6 1 src=10.100.0.5 dst=8.8.8.8 sport=1 dport=443 src=8.8.8.8 dst=192.0.2.10 sport=443 dport=bad"},
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
			if tt.want && (got.replyDestinationIP != tt.replyDestination || (got.replyDestinationPort != nil) != (tt.replyDestination != "")) {
				t.Fatalf("reply destination = %#v", got)
			}
			if tt.want && got.replyDestinationPort != nil && *got.replyDestinationPort != tt.replyPort {
				t.Fatalf("reply port = %d, want %d", *got.replyDestinationPort, tt.replyPort)
			}
		})
	}
}

type replayFlowOwners struct {
	cutover time.Time
}

func (r replayFlowOwners) LookupFlowOwner(ip string, at time.Time) (fcvm.FlowOwner, bool) {
	if ip != "10.100.0.5" {
		return fcvm.FlowOwner{}, false
	}
	if at.Before(r.cutover) {
		return fcvm.FlowOwner{InstanceID: "instance-a", AccountID: "account-a", AppID: "app-a", DeploymentID: "deployment-a"}, true
	}
	return fcvm.FlowOwner{InstanceID: "instance-b", AccountID: "account-b", AppID: "app-b", DeploymentID: "deployment-b"}, true
}

func TestReplayConntrackEventsAcrossIPReuse(t *testing.T) {
	cutover := time.Unix(1790500001, 0).UTC()
	lines := strings.Join([]string{
		"[1790500000.123456] [NEW] tcp 6 120 SYN_SENT src=10.100.0.5 dst=93.184.216.34 sport=42301 dport=443 [UNREPLIED] src=93.184.216.34 dst=192.0.2.10 sport=443 dport=50888",
		"[1790500001.123456] [NEW] udp 17 30 src=10.100.0.5 dst=198.51.100.20 sport=53000 dport=53 src=198.51.100.20 dst=192.0.2.10 sport=53 dport=53001",
		"[1790500001.123457] [NEW] tcp 6 120 src=10.100.0.6 dst=198.51.100.20 sport=40000 dport=443 src=198.51.100.20 dst=192.0.2.10 sport=443 dport=40000",
	}, "\n")
	queue := make(chan state.OutboundFlowEvent, 3)
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	coverage := newFlowCaptureCoverage(nil, "node", log)
	err := consumeConntrackEvents(strings.NewReader(lines), replayFlowOwners{cutover}, "node", netip.MustParsePrefix("10.100.0.0/16"), queue, log, coverage)
	if err != nil {
		t.Fatal(err)
	}
	if len(queue) != 2 || coverage.unattributed.Load() != 1 {
		t.Fatalf("recorded=%d unattributed=%d, want 2 and 1", len(queue), coverage.unattributed.Load())
	}
	first, second := <-queue, <-queue
	if first.AccountID != "account-a" || first.InstanceID != "instance-a" || first.ReplyDestinationIP != "192.0.2.10" || first.ReplyDestinationPort == nil || *first.ReplyDestinationPort != 50888 {
		t.Fatalf("first flow = %#v", first)
	}
	if second.AccountID != "account-b" || second.InstanceID != "instance-b" || second.ReplyDestinationPort == nil || *second.ReplyDestinationPort != 53001 {
		t.Fatalf("reused address flow = %#v", second)
	}
}
