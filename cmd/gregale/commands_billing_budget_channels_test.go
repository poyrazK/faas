package main_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/api"
)

// adr: 20261019193500 — the CLI shows, replaces and clears a budget's alert
// channels, and rejects malformed ids before any request.
func TestFinancialBudgetChannelsCLI(t *testing.T) {
	id := "6dc4f678-5766-4a06-a061-845c2b133fdd"
	channel := "0b3f8f1e-3d1c-4a3e-9a7e-1f2d3c4b5a69"
	var mu sync.Mutex
	var bodies []api.SetFinancialBudgetChannelsRequest
	var methods []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		if r.URL.Path != "/v1/billing/budgets/"+id+"/channels" {
			t.Errorf("path = %s", r.URL.Path)
		}
		methods = append(methods, r.Method)
		out := api.FinancialBudgetChannelsResponse{PolicyID: id, Channels: []api.NotificationChannelResponse{}}
		if r.Method == http.MethodPut {
			if r.Header.Get("Idempotency-Key") == "" {
				t.Error("PUT without Idempotency-Key")
			}
			var body api.SetFinancialBudgetChannelsRequest
			_ = json.NewDecoder(r.Body).Decode(&body)
			bodies = append(bodies, body)
			for _, c := range body.ChannelIDs {
				out.Channels = append(out.Channels, api.NotificationChannelResponse{ID: c, Name: "ops", Kind: "slack", Target: "T1/B2"})
			}
		}
		_ = json.NewEncoder(w).Encode(out)
	}))
	defer srv.Close()
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_live_x")
	stdout, restore := main.CaptureStdout(t)
	defer restore()
	_, restoreErr := main.CaptureStderr(t)
	defer restoreErr()

	run := func(args ...string) (int, string) {
		offset := len(stdout.Bytes())
		code := main.CmdBillingBudgets(append([]string{"channels"}, args...))
		return code, string(stdout.Bytes()[offset:])
	}
	if code, out := run(id); code != 0 || !strings.Contains(out, "account email only") {
		t.Fatalf("get: %d %q", code, out)
	}
	if code, out := run("--set", channel, id); code != 0 || !strings.Contains(out, "ops") || !strings.Contains(out, "slack") {
		t.Fatalf("set: %d %q", code, out)
	}
	if code, _ := run("--clear", id); code != 0 {
		t.Fatalf("clear: %d", code)
	}
	if code, _ := run("--set", "not-a-uuid", id); code == 0 {
		t.Fatal("malformed channel id accepted")
	}
	if code, _ := run("--set", channel, "--clear", id); code == 0 {
		t.Fatal("--set with --clear accepted")
	}
	mu.Lock()
	defer mu.Unlock()
	if strings.Join(methods, ",") != "GET,PUT,PUT" || len(bodies) != 2 || len(bodies[0].ChannelIDs) != 1 || bodies[0].ChannelIDs[0] != channel || bodies[1].ChannelIDs == nil || len(bodies[1].ChannelIDs) != 0 {
		t.Fatalf("requests = %v %+v", methods, bodies)
	}
}
