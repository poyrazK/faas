package faas

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
	"time"
)

func TestApplicationStandardSDKAssignmentInventory(t *testing.T) {
	const id = "00000000-0000-4000-8000-000000000001"
	stamp, err := time.Parse(time.RFC3339Nano, "2026-10-04T12:00:00.123456Z")
	if err != nil {
		t.Fatal(err)
	}
	record := ApplicationStandardAssignment{ID: id, OrgID: id, Scope: "organization", ScopeID: id, StandardID: id, AdmissionVersion: 2, Revision: 3, Active: false, CreatedBy: id, CreatedAt: stamp, UpdatedAt: stamp}
	paths := []string{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.Method+" "+r.URL.RequestURI())
		if r.Header.Get("Authorization") != "Bearer fixture" {
			t.Error("missing authentication")
		}
		w.Header().Set("Content-Type", "application/json")
		if r.URL.RawQuery != "" {
			_ = json.NewEncoder(w).Encode(ApplicationStandardAssignmentList{Assignments: []ApplicationStandardAssignment{record}, NextPageAfter: id})
		} else {
			_ = json.NewEncoder(w).Encode(record)
		}
	}))
	defer server.Close()
	client, err := NewClient(server.URL, "fixture")
	if err != nil {
		t.Fatal(err)
	}
	got, err := client.GetApplicationStandardAssignment(t.Context(), "acme", id)
	if err != nil || !reflect.DeepEqual(got, record) {
		t.Fatalf("retained assignment: %+v %v", got, err)
	}
	list, err := client.ListApplicationStandardAssignments(t.Context(), "acme", id, 1)
	if err != nil || len(list.Assignments) != 1 || !reflect.DeepEqual(list.Assignments[0], record) || list.NextPageAfter != id {
		t.Fatalf("paged inventory: %+v %v", list, err)
	}
	base := "GET /v1/orgs/acme/application-standard-assignments"
	if !reflect.DeepEqual(paths, []string{base + "/" + id, base + "?after=" + id + "&limit=1"}) {
		t.Fatalf("inventory paths: %v", paths)
	}
}
