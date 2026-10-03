package faas_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	faas "github.com/poyrazK/faas/sdk/go"
)

func TestRouteInvestigationSDKPreservesSelectionAndEvidence(t *testing.T) {
	body, err := os.ReadFile("../../tests/fixtures/route-investigation.json")
	if err != nil {
		t.Fatal(err)
	}
	customer := "00000000-0000-4000-8000-000000000004"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" || r.URL.Path != "/v1/apps/demo/route-health/deployments/candidate/investigation" || r.URL.Query().Get("path") != "/checkout" || r.URL.Query().Get("method") != "POST" || r.URL.Query().Get("status_code") != "403" || r.URL.Query().Get("customer_group_by") != "consumer" || r.URL.Query().Get("customer_id") != customer {
			t.Error("investigation scope binding")
		}
		_, _ = w.Write(body)
	}))
	defer server.Close()
	client, err := faas.NewClient(server.URL, "token")
	if err != nil {
		t.Fatal(err)
	}
	out, err := client.GetRouteHealthInvestigation(context.Background(), "demo", "candidate", faas.RouteHealthInvestigationOptions{Method: "POST", Path: "/checkout", StatusCode: 403, CustomerGroupBy: "consumer", CustomerID: customer})
	if err != nil {
		t.Fatal(err)
	}
	if out.Status != "regressed" || out.Report.Status != "healthy" || out.Selection.CustomerID != customer || out.Windows[0].Candidate.Examples[0].RepresentedRequests != 20 || out.Windows[0].Stable.ObservedRows != 1 {
		t.Fatal("advisory context or weighted examples lost")
	}
}
