package faas

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
	"time"
)

func TestApplicationStandardSDKMutations(t *testing.T) {
	const id = "00000000-0000-4000-8000-000000000001"
	expires := time.Date(2026, 10, 5, 12, 0, 0, 0, time.UTC)
	local := SetApplicationStandardLocalIntentRequest{ExpectedRevision: 1, Settings: json.RawMessage(`{}`), AdditionalLogDestinations: []string{}}
	approval := ApproveApplicationStandardExceptionRequest{ExpectedRevision: 2, StandardID: id, Version: 1, Field: "require_signed", Value: json.RawMessage(`false`), Reason: "Maintenance", ExpiresAt: expires}
	revoke := RevokeApplicationStandardExceptionRequest{ExpectedRevision: 3}
	base := "/v1/orgs/acme/application-standard-enrollments/" + id
	calls := []string{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls = append(calls, r.Method+" "+r.URL.Path)
		if r.Header.Get("Authorization") != "Bearer fixture" {
			t.Error("missing authentication")
		}
		var got any
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Error(err)
		}
		var request any
		switch r.URL.Path {
		case base + "/local-intent":
			request = local
		case base + "/exceptions":
			request = approval
		case base + "/exceptions/" + id + "/revoke":
			request = revoke
		default:
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		raw, _ := json.Marshal(request)
		var want any
		_ = json.Unmarshal(raw, &want)
		if !reflect.DeepEqual(got, want) {
			t.Errorf("SDK changed explicit false/empty intent: %+v", got)
		}
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == base+"/local-intent" {
			_ = json.NewEncoder(w).Encode(ApplicationStandardEnrollment{AppID: id, State: "pending", DesiredRevision: 2, PersistedRevision: 1})
		} else {
			status := "active"
			if r.URL.Path == base+"/exceptions/"+id+"/revoke" {
				status = "revoked"
			}
			_ = json.NewEncoder(w).Encode(ApplicationStandardException{ID: id, Status: status, Value: json.RawMessage(`false`), ExpiresAt: expires})
		}
	}))
	defer server.Close()
	client, err := NewClient(server.URL, "fixture")
	if err != nil {
		t.Fatal(err)
	}
	enrollment, err := client.SetApplicationStandardLocalIntent(t.Context(), "acme", id, local)
	if err != nil || enrollment.DesiredRevision != 2 || enrollment.PersistedRevision != 1 || enrollment.ObservedRevision != 0 {
		t.Fatalf("intent: %+v %v", enrollment, err)
	}
	x, err := client.ApproveApplicationStandardException(t.Context(), "acme", id, approval)
	if err != nil || x.Status != "active" || string(x.Value) != `false` || !x.ExpiresAt.Equal(expires) {
		t.Fatalf("approval: %+v %v", x, err)
	}
	x, err = client.RevokeApplicationStandardException(t.Context(), "acme", id, id, revoke)
	if err != nil || x.Status != "revoked" {
		t.Fatalf("revocation: %+v %v", x, err)
	}
	want := []string{"PUT " + base + "/local-intent", "POST " + base + "/exceptions", "POST " + base + "/exceptions/" + id + "/revoke"}
	if !reflect.DeepEqual(calls, want) {
		t.Fatalf("mutation routes: %+v", calls)
	}
}
