package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestVerificationRunListClientFilters(t *testing.T) {
	calls := 0
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Method != "GET" {
			t.Fatal("list wrote")
		}
		if r.URL.Query().Get("deployment_id") != "22222222-2222-4222-8222-222222222222" || r.URL.Query().Get("cursor") != "a+/=" || r.URL.Query().Get("limit") != "2" {
			t.Errorf("query: %s", r.URL.RawQuery)
		}
		_ = json.NewEncoder(w).Encode(BindingVerificationRunList{Runs: []BindingVerificationRunSummary{}, ObservedAt: time.Now(), NextCursor: "next"})
	}))
	defer s.Close()
	c := NewClient(s.URL, "token")
	o := BindingVerificationRunListOptions{Limit: 2, Cursor: "a+/=", DeploymentID: "22222222-2222-4222-8222-222222222222"}
	p, e := c.ListBindingVerificationRuns(context.Background(), "app", o)
	if e != nil || p.NextCursor != "next" {
		t.Fatal(e)
	}
	p, e = c.ListProjectBindingVerificationRuns(context.Background(), "project", "production", o)
	if e != nil || p.Runs == nil || calls != 2 {
		t.Fatal(e)
	}
}
