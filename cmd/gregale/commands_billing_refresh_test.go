// adr:383
package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestFOCUSCLIRefreshInvoice(t *testing.T) {
	const id = "c4979a3e-345b-4a96-a635-321589233f7f"
	calls := 0
	fail := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Method != http.MethodPost || r.URL.Path != "/v1/invoices/"+id+"/refresh" || r.Header.Get("Authorization") != "Bearer fp_live_x" {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL)
		}
		if fail {
			api.WriteProblem(w, api.NewProblem(http.StatusConflict, api.CodeConflict, "Refresh conflict", "Snapshot changed"))
			return
		}
		_ = json.NewEncoder(w).Encode(api.InvoiceRefreshResponse{InvoiceID: id, Provider: "polar", SourceGap: "unclassified", LineItems: 2, UpdatedAt: time.Now().UTC()})
	}))
	defer srv.Close()
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_live_x")
	stdout, restore := captureStdout(t)
	defer restore()
	stderr, restoreErr := captureStderr(t)
	defer restoreErr()
	if code := cmdBilling([]string{"refresh-invoice", id}); code != 0 {
		t.Fatalf("exit=%d stderr=%s", code, stderr)
	}
	var result api.InvoiceRefreshResponse
	if json.Unmarshal(stdout.Bytes(), &result) != nil || result.InvoiceID != id || result.SourceGap != "unclassified" || result.Detailed {
		t.Fatalf("output=%s", stdout)
	}
	for _, args := range [][]string{{}, {id, "extra"}, {"--provider", "stripe", id}} {
		if code := cmdBillingRefreshInvoice(args); code == 0 {
			t.Fatalf("accepted invalid args=%v", args)
		}
	}
	if calls != 1 {
		t.Fatal("invalid CLI arguments reached API")
	}
	fail = true
	before := stdout.String()
	if code := cmdBillingRefreshInvoice([]string{id}); code == 0 || stdout.String() != before {
		t.Fatal("failed refresh printed success")
	}
}
