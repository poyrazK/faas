package main_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	main "github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/api"
)

func r63Server(t *testing.T, handler http.HandlerFunc) {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	t.Setenv("FAAS_API", server.URL)
	t.Setenv("FAAS_TOKEN", "test-token")
}

func r63Run(t *testing.T, args ...string) (int, string) {
	t.Helper()
	main.ResetJSONOut(t)
	stdoutBuf, stderr, restore := main.SwapIO(t)
	code := main.Run(args)
	restore()
	return code, stdoutBuf.String() + stderr()
}

// Prod hunt r63: `audit list` printed time, actor, kind and the account
// UUID, never the event id `audit get` takes; passing the UUID answered
// "id must be a positive integer".
func TestAuditListPrintsTheIDAuditGetTakes(t *testing.T) {
	gets := 0
	r63Server(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/v1/audit-events":
			_ = json.NewEncoder(w).Encode(api.ListAuditEventsResponse{Events: []api.AuditEventResponse{{
				ID: "4821", At: "2026-11-27T11:45:01Z", Actor: "schedd", Kind: "cron.fired", Subject: "47434ef3-65c5-42c4-997f-80fe99be71cf",
			}}})
		case strings.HasPrefix(r.URL.Path, "/v1/audit-events/"):
			gets++
			_ = json.NewEncoder(w).Encode(api.AuditEventResponse{ID: "4821", Kind: "cron.fired"})
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		}
	})
	code, out := r63Run(t, "audit", "list")
	if code != 0 || !strings.HasPrefix(out, "4821\t2026-11-27T11:45:01Z\tschedd\tcron.fired\t") {
		t.Fatalf("audit list = %d:\n%s", code, out)
	}
	code, out = r63Run(t, "audit", "get", "47434ef3-65c5-42c4-997f-80fe99be71cf")
	if code == 0 || !strings.Contains(out, "first column of `gregale audit list`") || gets != 0 {
		t.Fatalf("audit get <uuid> = %d (gets %d):\n%s", code, gets, out)
	}
	if code, out = r63Run(t, "audit", "get", "4821"); code != 0 || gets != 1 {
		t.Fatalf("audit get 4821 = %d (gets %d):\n%s", code, gets, out)
	}
}

// Prod hunt r63: `alert slo create <app>` takes the app first, but `slo
// delete <app> <id>` and `slo status <app> <id>` printed usage.
func TestSLODeleteAndStatusTakeTheAppFirst(t *testing.T) {
	const id = "e611f0f7-6c57-4a9f-a7e3-d442c9573798"
	var paths []string
	r63Server(t, func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.Method+" "+r.URL.Path)
		if r.Method == http.MethodGet {
			_ = json.NewEncoder(w).Encode(api.SLOResponse{ID: id, Name: "avail", WindowDays: 30, ObjectivePct: 99.5})
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
	if code, out := r63Run(t, "alert", "slo", "status", "web", id); code != 0 {
		t.Fatalf("slo status web <id> = %d:\n%s", code, out)
	}
	if code, out := r63Run(t, "alert", "slo", "delete", "web", id); code != 0 || !strings.Contains(out, "Deleted SLO "+id) {
		t.Fatalf("slo delete web <id> = %d:\n%s", code, out)
	}
	if len(paths) != 2 || !strings.Contains(paths[0], "/v1/apps/web/slos/"+id) || !strings.HasPrefix(paths[1], "DELETE /v1/apps/web/slos/"+id) {
		t.Fatalf("requests = %v", paths)
	}
}

// Prod hunt r63: `queue bindings list` on an app with none printed nothing.
func TestQueueBindingsListSaysWhenEmpty(t *testing.T) {
	r63Server(t, func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode([]api.QueueBindingResponse{})
	})
	code, out := r63Run(t, "queue", "bindings", "list", "web")
	if code != 0 || !strings.Contains(out, "No queue bindings for web") {
		t.Fatalf("queue bindings list = %d:\n%s", code, out)
	}
	code, out = r63Run(t, "webhook", "list", "web")
	if code != 0 || !strings.Contains(out, "No webhooks for web") {
		t.Fatalf("webhook list = %d:\n%s", code, out)
	}
}

// Prod hunt r63: `bucket usage` listed cost_millicents, the provider's own
// cost that never enters a charge, as an unknown meter beside the estimate.
func TestBucketUsageHidesProviderCostMeter(t *testing.T) {
	meters := []string{"cost_millicents"}
	r63Server(t, func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(api.ObjectStorageUsageResponse{
			Usage:   api.ObjectStorageUsage{UnavailableMeters: meters},
			Charges: &api.ObjectStorageCharge{Currency: "USD"}, BillingMode: "live",
		})
	})
	code, out := r63Run(t, "bucket", "usage")
	if code != 0 || strings.Contains(out, "Unavailable meters") || !strings.Contains(out, "Estimated charge: 0 millicents USD") {
		t.Fatalf("bucket usage = %d:\n%s", code, out)
	}
	meters = []string{"stored_byte_hours", "cost_millicents"}
	code, out = r63Run(t, "bucket", "usage")
	if code != 0 || !strings.Contains(out, "Unavailable meters (numeric values are unknown): stored_byte_hours\n") {
		t.Fatalf("bucket usage with an unmetered charge = %d:\n%s", code, out)
	}
}
