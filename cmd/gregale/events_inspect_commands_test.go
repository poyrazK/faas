package main_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/cmd/gregale"
)

// events inspect told the customer to add "this event's --source and --id"
// to the attempts and recover commands; it now prints them whole
// (Hunt 12 H12-46).
func TestEventsInspectPrintsRunnableCommands(t *testing.T) {
	main.ResetJSONOut(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/events/receipt" {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte(`{"event_id":"ev-1","event_source":"billing.h12","event_type":"invoice.paid","accepted_at":"2026-10-22T16:41:45Z","snapshot_captured":true,"recipient_count":1,
			"recipients":[{"app_slug":"h12-ev","subscription_id":"sub-1","routing":{"state":"enqueued","attempts":1},"attempt_history_url":"/v1/events/attempts?x","recovery_actions":[{"kind":"replay"}]}]}`))
	}))
	t.Cleanup(srv.Close)
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_test_x")
	out, restore := main.CaptureStdout(t)
	code := main.Run([]string{"events", "inspect", "--source", "billing.h12", "--id", "ev-1"})
	restore()
	got := out.String()
	for _, want := range []string{
		"Attempt history: gregale events attempts --subscription sub-1 --source billing.h12 --id ev-1\n",
		"Recover: gregale events recover --subscription sub-1 --source billing.h12 --id ev-1 (add --dry-run to preview)\n",
	} {
		if code != 0 || !strings.Contains(got, want) {
			t.Fatalf("exit %d, missing %q in:\n%s", code, want, got)
		}
	}
}

func TestEventsPublishNamesTheInspectCommand(t *testing.T) {
	main.ResetJSONOut(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/events:publish" {
			http.NotFound(w, r)
			return
		}
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte(`{"id":"ev-9","account_id":"acct-1","receipt_url":"/v1/events/receipt?id=ev-9&source=billing.h12"}`))
	}))
	t.Cleanup(srv.Close)
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_test_x")
	out, restore := main.CaptureStdout(t)
	code := main.Run([]string{"events", "publish", "billing.h12", "invoice.paid", "--data", `{"amount":250}`})
	restore()
	if want := "Inspect: gregale events inspect --source billing.h12 --id ev-9\n"; code != 0 || !strings.Contains(out.String(), want) {
		t.Fatalf("exit %d, want %q in:\n%s", code, want, out.String())
	}
}
