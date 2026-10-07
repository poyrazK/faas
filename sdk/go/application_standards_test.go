package faas

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
	"time"
)

func TestApplicationStandardSDKInspection(t *testing.T) {
	const id = "00000000-0000-4000-8000-000000000001"
	const timestamp = "2026-10-04T12:00:00Z"
	request := ApplicationStandardReviewRequest{AssignmentID: id, Scope: "organization", ScopeID: id, StandardID: id, AdmissionVersion: 2, ExpectedRevision: 1, Active: false, BatchSize: 10}
	calls := []string{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls = append(calls, r.Method+" "+r.URL.RequestURI())
		if r.Header.Get("Authorization") != "Bearer fixture" {
			t.Error("missing authentication")
		}
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodPost {
			var got ApplicationStandardReviewRequest
			if err := json.NewDecoder(r.Body).Decode(&got); err != nil || !reflect.DeepEqual(got, request) {
				t.Errorf("review request: %+v %v", got, err)
			}
		}
		switch {
		case r.URL.Path == "/v1/orgs/acme/application-standard-reviews" || r.URL.Path == "/v1/orgs/acme/application-standard-reviews/"+id:
			_ = json.NewEncoder(w).Encode(ApplicationStandardReview{ID: id, Request: request, Applications: []ApplicationStandardReviewedApp{{AppID: id, AfterAdoptions: []ApplicationStandardAdoption{{AssignmentID: id, Version: 2}}}}})
		case r.URL.Path == "/v1/orgs/acme/application-standard-operations/"+id:
			_ = json.NewEncoder(w).Encode(ApplicationStandardOperation{ID: id, State: "waiting", Targets: []ApplicationStandardOperationTarget{{AppID: id, State: "persisted", DesiredRevision: 2}}})
		case r.URL.Path == "/v1/orgs/acme/application-standard-enrollments/"+id+"/exceptions":
			_ = json.NewEncoder(w).Encode(ApplicationStandardExceptionList{Exceptions: []ApplicationStandardException{{ID: id, Status: "revoked", Reason: "Maintenance", Value: json.RawMessage(`[5432]`)}}})
		default:
			_, _ = w.Write([]byte(`{"app_id":"` + id + `","desired_revision":2,"persisted_revision":2,"observed_revision":0,"state":"persisted","installed_exception_expires_at":"` + timestamp + `"}`))
		}
	}))
	defer server.Close()
	client, err := NewClient(server.URL, "fixture")
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	p, err := client.PreviewApplicationStandardAssignment(ctx, "acme", request)
	if err != nil || p.Request.Active || p.Applications[0].AfterAdoptions[0].Version != 2 {
		t.Fatalf("preview: %+v %v", p, err)
	}
	if _, err := client.GetApplicationStandardReview(ctx, "acme", id); err != nil {
		t.Fatal(err)
	}
	o, err := client.GetApplicationStandardOperation(ctx, "acme", id)
	if err != nil || o.State != "waiting" || o.Targets[0].State != "persisted" {
		t.Fatalf("progress: %+v %v", o, err)
	}
	xs, err := client.ListApplicationStandardExceptions(ctx, "acme", id, id, 1)
	if err != nil || xs.Exceptions[0].Status != "revoked" || string(xs.Exceptions[0].Value) != `[5432]` {
		t.Fatalf("history: %+v %v", xs, err)
	}
	e, err := client.GetApplicationStandardEnrollment(ctx, "acme", id)
	if err != nil || e.ObservedRevision != 0 || e.InstalledExceptionExpiresAt == nil || e.InstalledExceptionExpiresAt.Format(time.RFC3339) != timestamp {
		t.Fatalf("projection: %+v %v", e, err)
	}
	want := []string{"POST /v1/orgs/acme/application-standard-reviews", "GET /v1/orgs/acme/application-standard-reviews/" + id, "GET /v1/orgs/acme/application-standard-operations/" + id, "GET /v1/orgs/acme/application-standard-enrollments/" + id + "/exceptions?after=" + id + "&limit=1", "GET /v1/orgs/acme/application-standard-enrollments/" + id}
	if !reflect.DeepEqual(calls, want) {
		t.Fatalf("paths %v", calls)
	}
}
