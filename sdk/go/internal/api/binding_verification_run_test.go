package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
)

func TestBindingVerificationRunClientPreservesIdentityConsentAndStep(t *testing.T) {
	id, dep := "b9aeef19-a494-4268-8f6d-bda5c8363625", "e746a535-dac5-49f6-abf4-c73cd2543aeb"
	request := CreateBindingVerificationRunRequest{ID: id, DeploymentID: dep, ReviewDigest: strings.Repeat("a", 64), TTLSeconds: 60, QueuePublish: true}
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		path := "/v1/apps/app/bindings/verification-runs"
		if r.URL.Path == path {
			var in CreateBindingVerificationRunRequest
			if r.Method != "POST" || json.NewDecoder(r.Body).Decode(&in) != nil || !reflect.DeepEqual(in, request) {
				t.Errorf("create request changed: %+v", in)
			}
		} else if !strings.HasPrefix(r.URL.Path, path+"/"+id) {
			t.Errorf("run identity changed: %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		if strings.HasSuffix(r.URL.Path, "/steps/2/admit") {
			_ = json.NewEncoder(w).Encode(BindingVerificationRunStep{ResourceID: dep, ResourceKind: "queue", Owned: true, Status: "published"})
		} else {
			_ = json.NewEncoder(w).Encode(BindingVerificationRun{ID: id, DeploymentID: dep, Status: "pending"})
		}
	}))
	defer server.Close()
	client := NewClient(server.URL, "test")
	ctx := context.Background()
	if out, err := client.CreateBindingVerificationRun(ctx, "app", request); err != nil || out.ID != id {
		t.Fatalf("create: %+v %v", out, err)
	}
	for _, method := range []func(context.Context, string, string) (BindingVerificationRun, error){client.GetBindingVerificationRun, client.ResumeBindingVerificationRun, client.CancelBindingVerificationRun, client.CompleteBindingVerificationRun} {
		if out, err := method(ctx, "app", id); err != nil || out.ID != id {
			t.Fatalf("run operation: %+v %v", out, err)
		}
	}
	if step, err := client.AdmitBindingVerificationRunStep(ctx, "app", id, 2); err != nil || !step.Owned || step.ResourceID != dep {
		t.Fatalf("step: %+v %v", step, err)
	}
	if calls != 6 {
		t.Fatalf("calls=%d", calls)
	}
}
