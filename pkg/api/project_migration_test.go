package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestProjectMigrationClientJournalContract(t *testing.T) {
	const id = "7a7ab9c4-2995-48d5-85c5-c094ce55c599"
	var requests []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests = append(requests, r.Method+" "+r.URL.RequestURI())
		if r.Method == http.MethodPost {
			var body map[string]json.RawMessage
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Error(err)
			}
			if _, ok := body["plan_token"]; !ok {
				t.Error("reviewed source token missing")
			}
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"` + id + `","project":"shop","services":[],"migrations":[]}`))
	}))
	defer server.Close()
	c := NewClient(server.URL, "key")
	ctx := t.Context()
	if _, err := c.CreateProjectMigration(ctx, CreateProjectMigrationRequest{ID: id, PlanToken: "reviewed", Only: []string{"api", "web"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := c.GetProjectMigration(ctx, id); err != nil {
		t.Fatal(err)
	}
	if _, err := c.ListProjectMigrations(ctx, "shop"); err != nil {
		t.Fatal(err)
	}
	if _, err := c.ResumeProjectMigration(ctx, id, ResumeProjectMigrationRequest{PlanToken: "same-source", ExpectedVersion: 4, Only: []string{"api"}}); err != nil {
		t.Fatal(err)
	}
	want := []string{"POST /v1/project-migrations", "GET /v1/project-migrations/" + id, "GET /v1/project-migrations?project=shop", "POST /v1/project-migrations/" + id + "/resume"}
	for i, path := range want {
		if requests[i] != path {
			t.Fatalf("route %d: %s", i, requests[i])
		}
	}
	for _, bad := range []string{"", "../other", "00000000-0000-0000-0000-000000000000", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"} {
		if ValidProjectMigrationID(bad) {
			t.Fatalf("invalid identity accepted: %s", bad)
		}
	}
}
