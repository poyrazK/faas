package main_test

import (
	"bytes"
	"github.com/onebox-faas/faas/cmd/gregale"
	"net/http"
	"strings"
	"testing"
)

func TestCmdRealtimeConnectionsListsFilteredInventory(t *testing.T) {
	main.ResetJSONOut(t)
	f := main.AuthedFakeAPI(t, `{"connections":[{"id":"conn-a","endpoint_id":"endpoint-1","app_id":"app-1","account_id":"account-1","principal":"user-a","connected_at":"2026-09-17T12:00:00Z","last_seen_at":"2026-09-17T12:02:00Z","expires_at":"2026-09-17T13:00:00Z","channels":["room-a"]}],"limit":25,"truncated":true,"next_cursor":"next-token","partial":true,"nodes_queried":1,"nodes_unavailable":1}`, http.StatusOK)
	oldOut, oldErr := (*main.OsStdout), (*main.OsStderr)
	var out, stderr bytes.Buffer
	(*main.OsStdout), (*main.OsStderr) = &out, &stderr
	t.Cleanup(func() {
		(*main.OsStdout), (*main.OsStderr) = oldOut, oldErr
	})

	if code := main.CmdRealtimeConnections([]string{"demo", "endpoint-1", "--channel", "room-a", "--principal", "user-a", "--limit", "25", "--cursor", "previous-token"}); code != 0 {
		t.Fatalf("exit = %d, output = %s", code, out.String())
	}
	if (*f.SawMethod()) != http.MethodGet || (*f.SawPath()) != "/v1/apps/demo/realtime/endpoints/endpoint-1/connections" {
		t.Fatalf("route = %s %s", (*f.SawMethod()), (*f.SawPath()))
	}
	if (*f.SawQuery()) != "channel=room-a&cursor=previous-token&limit=25&principal=user-a" {
		t.Fatalf("query = %q", (*f.SawQuery()))
	}
	if !strings.Contains(out.String(), "conn-a") || !strings.Contains(out.String(), "user-a") {
		t.Fatalf("connection missing from output: %s", out.String())
	}
	if !strings.Contains(out.String(), "use --cursor next-token to continue") {
		t.Fatalf("cursor continuation missing: %s", out.String())
	}
	if !strings.Contains(stderr.String(), "1 node(s) unavailable") {
		t.Fatalf("partial warning missing: %s", stderr.String())
	}
}
