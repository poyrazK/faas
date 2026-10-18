package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestBindingVerificationReviewCreateResume(t *testing.T) {
	const deployment = "22222222-2222-4222-8222-222222222222"
	const digest = "opaque-server-digest"
	const id = "11111111-1111-4111-8111-111111111111"
	base := "/v1/apps/app/bindings/verification-runs"
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case base + "/review":
			var in ReviewBindingVerificationRequest
			if err := json.NewDecoder(r.Body).Decode(&in); err != nil || in.DeploymentID != "" {
				t.Errorf("live review input: %+v %v", in, err)
			}
			_ = json.NewEncoder(w).Encode(BindingVerificationReview{Ready: true, DeploymentID: deployment, ServiceTargets: map[string]string{"billing": deployment}, ReviewDigest: digest})
		case base:
			var in CreateBindingVerificationRunRequest
			if err := json.NewDecoder(r.Body).Decode(&in); err != nil || in.DeploymentID != deployment || in.ReviewDigest != digest || in.ServiceTargets["billing"] != deployment {
				t.Errorf("review forwarding: %+v %v", in, err)
			}
			_ = json.NewEncoder(w).Encode(BindingVerificationRun{ID: id, ReviewDigest: digest})
		case base + "/" + id + "/resume":
			_ = json.NewEncoder(w).Encode(BindingVerificationRun{ID: id, ReviewDigest: digest})
		default:
			t.Errorf("unexpected route: %s", r.URL.Path)
		}
	}))
	defer server.Close()
	client := NewClient(server.URL, "test")
	review, err := client.ReviewBindingVerification(testContext(t), "app", ReviewBindingVerificationRequest{})
	if err != nil || !review.Ready {
		t.Fatalf("review: %+v %v", review, err)
	}
	run, err := client.CreateBindingVerificationRun(testContext(t), "app", CreateBindingVerificationRunRequest{ID: id, DeploymentID: review.DeploymentID, ServiceTargets: review.ServiceTargets, ReviewDigest: review.ReviewDigest, TTLSeconds: 60})
	if err != nil {
		t.Fatal(err)
	}
	resumed, err := client.ResumeBindingVerificationRun(testContext(t), "app", run.ID)
	if err != nil || resumed.ID != run.ID || calls != 3 {
		t.Fatalf("resume: %+v %v calls=%d", resumed, err, calls)
	}
}
