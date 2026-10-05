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

func TestRouteLatencyInvestigationSDKPreservesMeasuredZeroAndComparisons(t *testing.T) {
	body, err := os.ReadFile("../../tests/fixtures/route-latency-investigation.json")
	if err != nil {
		t.Fatal(err)
	}
	customer := "00000000-0000-4000-8000-000000000004"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if r.Method != "GET" || q.Get("signal") != "latency" || q.Get("status_code") != "0" || q.Get("method") != "POST" || q.Get("path") != "/checkout" || q.Get("customer_id") != customer || q.Get("customer_group_by") != "consumer" {
			t.Error("latency scope binding")
		}
		_, _ = w.Write(body)
	}))
	defer server.Close()
	client, err := faas.NewClient(server.URL, "token")
	if err != nil {
		t.Fatal(err)
	}
	r, err := client.GetRouteHealthInvestigation(context.Background(), "demo", "candidate", faas.RouteHealthInvestigationOptions{Method: "POST", Path: "/checkout", Signal: "latency", CustomerGroupBy: "consumer", CustomerID: customer})
	if err != nil {
		t.Fatal(err)
	}
	d := r.Windows[0].Diagnostics
	if r.Status != "regressed" || r.Finding.ErrorStatus != "healthy" || r.Selection.Signal != "latency" || d == nil || d.Candidate.GuestP95MS == nil || *d.Candidate.GuestP95MS != 0 || d.Candidate.WakeSamples != 1 || *d.Dependencies[0].P95DeltaMS != 460 || d.Dependencies[0].Candidate.RepresentedCalls != 132 {
		t.Fatal("latency, weighted or measured-zero evidence lost")
	}
}
