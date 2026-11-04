// adr: 20261104144000
// adr: 20261104173000
package faas

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestServiceAbortReceiptTransport(t *testing.T) {
	for _, scope := range []string{"default", "staging"} {
		t.Run(scope, func(t *testing.T) {
			attempt := strings.Repeat("a", 32)
			review := AppReleaseReview{AppID: "app", ConfigurationHash: "review", Scope: scope, LatestDeploymentID: "candidate", ServingDeploymentIDs: []string{"predecessor"}}
			if scope != "default" {
				review.ProjectID, review.EnvironmentID = "project", "environment"
			}
			backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				if r.Method == http.MethodPost {
					if r.Header.Get(ServiceAbortAttemptHeader) != attempt {
						t.Error("missing service abort attempt")
					}
					var req RecoverRolloutRequest
					if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.ExpectedReview == nil || req.ExpectedReview.ConfigurationHash != review.ConfigurationHash || req.ExpectedReview.Scope != scope || req.ExpectedReview.ProjectID != review.ProjectID || req.ExpectedReview.EnvironmentID != review.EnvironmentID || req.DeploymentID != "candidate" || req.ExpectedPredecessorDeploymentID != "predecessor" {
						t.Error("lost reviewed exact request")
					}
					w.WriteHeader(http.StatusAccepted)
					_ = json.NewEncoder(w).Encode(RolloutTransitionResponse{ServiceRecovery: &ServiceRolloutRecoveryReceipt{DeploymentID: "candidate", PredecessorDeploymentID: "predecessor", RequestID: "native-request", Status: "accepted"}})
				} else {
					if r.Header.Get(ServiceAbortAttemptHeader) != "" || r.Header.Get("Idempotency-Key") != "" {
						t.Error("receipt read leaked mutation metadata")
					}
					if r.URL.Path == "/v1/apps/alpha/release-review" {
						_ = json.NewEncoder(w).Encode(review)
						return
					}
					if r.URL.Path != "/v1/apps/alpha/service-aborts/"+attempt {
						t.Error("wrong receipt lookup")
					}
					_ = json.NewEncoder(w).Encode(ServiceAbortReceipt{AttemptID: attempt, RequestID: "native-request", Scope: scope, ProjectID: review.ProjectID, EnvironmentID: review.EnvironmentID})
				}
			}))
			defer backend.Close()
			client, err := NewClient(backend.URL, "token")
			if err != nil {
				t.Fatal(err)
			}
			ctx := ContextWithServiceAbortAttempt(WithIdempotencyKey(t.Context(), IdempotencyKey("private-key")), attempt)
			if got, err := client.RecoverReviewedServiceRollout(ctx, "alpha", "candidate", "predecessor", "recover", review); err != nil || got.ServiceRecovery == nil || got.ServiceRecovery.RequestID != "native-request" {
				t.Fatalf("accepted result: %+v %v", got, err)
			}
			if got, err := client.GetAppReleaseReview(ctx, "alpha"); err != nil || got.ConfigurationHash != review.ConfigurationHash {
				t.Fatalf("review: %+v %v", got, err)
			}
			if receipt, err := client.GetServiceAbortReceipt(ctx, "alpha", attempt); err != nil || receipt.AttemptID != attempt || receipt.Scope != scope || receipt.ProjectID != review.ProjectID || receipt.EnvironmentID != review.EnvironmentID {
				t.Fatalf("receipt: %+v %v", receipt, err)
			}
		})
	}
}
