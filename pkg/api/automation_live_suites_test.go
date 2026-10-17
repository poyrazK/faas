// adr: 1296
package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

func TestAutomationLiveSuiteClientMetadataRoutes(t *testing.T) {
	var calls []string
	var mu sync.Mutex
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		calls = append(calls, r.Method+" "+r.URL.EscapedPath())
		w.Header().Set("Content-Type", "application/json")
		if r.Header.Get("Authorization") != "Bearer test-token" {
			t.Error("missing authentication")
		}
		if r.Method == http.MethodPut {
			var body SaveAutomationLiveSuiteRequest
			decoder := json.NewDecoder(r.Body)
			decoder.DisallowUnknownFields()
			if decoder.Decode(&body) != nil || len(body.Cases) != 2 || body.Cases[1].RunID != "" || body.ExpectedVersion != 7 {
				t.Error("save sent more than the original selection/run links")
			}
		}
		if strings.HasSuffix(r.URL.Path, "/suite-id") {
			_, _ = w.Write([]byte(`{"id":"suite-id","name":"case/report","outcome":"incomplete","cases":[],"input":"PRIVATE"}`))
		} else {
			_, _ = w.Write([]byte(`{"suites":[]}`))
		}
	}))
	defer srv.Close()
	c := NewClient(srv.URL, "test-token")
	request := SaveAutomationLiveSuiteRequest{ExpectedVersion: 7, ExpectedPublishedVersion: 4, ExpectedDeploymentID: "deployment", Cases: []AutomationLiveSuiteMember{{Name: "first", RunID: "run-id"}, {Name: "second"}}}
	view, err := c.SaveAutomationLiveSuite(context.Background(), "billing", "case/report", "suite-id", request)
	if err != nil || view.Outcome != "incomplete" {
		t.Fatal(view, err)
	}
	if _, err := c.GetAutomationLiveSuite(context.Background(), "billing", "case/report", "suite-id"); err != nil {
		t.Fatal(err)
	}
	if _, err := c.ListAutomationLiveSuites(context.Background(), "billing", "case/report"); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(calls) != 3 || !strings.Contains(calls[0], "case%2Freport/live-suites/suite-id") || !strings.HasPrefix(calls[1], "GET ") || !strings.HasPrefix(calls[2], "GET ") {
		t.Fatal("unexpected suite operations", calls)
	}
	raw, _ := json.Marshal(view)
	if strings.Contains(string(raw), "PRIVATE") {
		t.Fatal("unknown payload fields entered typed suite report")
	}
}
