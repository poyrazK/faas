// adr:383
package api

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestFOCUSClientRefreshFacts(t *testing.T) {
	const id = "c4979a3e-345b-4a96-a635-321589233f7f"
	now := time.Now().UTC().Truncate(time.Second)
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		raw, err := io.ReadAll(r.Body)
		if err != nil || len(raw) != 0 || r.Method != http.MethodPost || r.URL.Path != "/v1/invoices/"+id+"/refresh" || r.URL.RawQuery != "" || r.Header.Get("Authorization") != "Bearer test-token" {
			t.Errorf("unexpected refresh: %s %s body=%q error=%v", r.Method, r.URL, raw, err)
		}
		if calls == 1 {
			_ = json.NewEncoder(w).Encode(InvoiceRefreshResponse{InvoiceID: id, Provider: "stripe", Detailed: true, LineItems: 2, UpdatedAt: now})
		} else {
			WriteProblem(w, NewProblem(http.StatusConflict, CodeConflict, "Refresh conflict", "Snapshot changed"))
		}
	}))
	defer srv.Close()
	c := NewClient(srv.URL, "test-token")
	originalTimeout := c.http.Timeout
	result, err := c.RefreshInvoiceFacts(t.Context(), id)
	if err != nil || result.InvoiceID != id || !result.Detailed || result.LineItems != 2 || !result.UpdatedAt.Equal(now) {
		t.Fatalf("result=%+v error=%v", result, err)
	}
	if c.http.Timeout != originalTimeout {
		t.Fatal("refresh mutated the shared client")
	}
	_, err = c.RefreshInvoiceFacts(t.Context(), id)
	var problem *APIError
	if !errors.As(err, &problem) || problem.Problem.Code != CodeConflict {
		t.Fatalf("lost provider conflict: %v", err)
	}
}
