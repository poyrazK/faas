package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestPrintRealtimeOccupancy(t *testing.T) {
	var out bytes.Buffer
	prev := osStdout
	osStdout = &out
	t.Cleanup(func() { osStdout = prev })
	printRealtimeOccupancy(api.ManagedRealtimeOccupancyResponse{Channels: []api.ManagedRealtimeChannelOccupancy{
		{Channel: "lobby", Subscribers: 12401, PresenceMembers: 980},
		{Channel: "a-much-longer-channel-name", Subscribers: 0},
	}})
	lines := strings.Split(strings.TrimRight(out.String(), "\n"), "\n")
	if len(lines) != 3 || !strings.HasPrefix(lines[0], "CHANNEL ") || !strings.Contains(lines[0], "SUBSCRIBERS") || !strings.Contains(lines[0], "PRESENCE MEMBERS") {
		t.Fatalf("header = %q", out.String())
	}
	if !strings.HasPrefix(lines[1], "lobby ") || !strings.Contains(lines[1], "12,401") || !strings.Contains(lines[1], "980") {
		t.Fatalf("lobby row = %q", lines[1])
	}
	if len(lines[1]) != len(lines[2]) {
		t.Fatalf("rows are not aligned:\n%s", out.String())
	}
}

func TestRealtimeOccupancyRejectsBadArguments(t *testing.T) {
	var errOut bytes.Buffer
	prev := osStderr
	osStderr = &errOut
	t.Cleanup(func() { osStderr = prev })
	tooMany := append([]string{"app", "ep"}, strings.Fields(strings.Repeat("c ", api.RealtimeOccupancyChannelsMax+1))...)
	for name, args := range map[string][]string{
		"no channel":      {"app", "ep"},
		"too many":        tooMany,
		"invalid channel": {"app", "ep", "a/b"},
	} {
		if code := cmdRealtimeOccupancy(args); code == 0 {
			t.Fatalf("%s: exit 0, want an error", name)
		}
	}
}
