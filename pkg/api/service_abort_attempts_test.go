// adr: 20261104144000
package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestServiceAbortAttemptTransportAndHash(t *testing.T) {
	const app = "ffdf536b-2bcb-4409-99b8-af9025b20145"
	const candidate = "356f0b3b-6787-4131-aaac-ddf1e37d6f49"
	const predecessor = "23a15b9b-c514-4fb6-9e8b-3ae104a48ef4"
	review := AppReleaseReview{AppID: app, Scope: DefaultEnvScope, LatestDeploymentID: candidate, ServingDeploymentIDs: []string{predecessor}, ConfigurationHash: "review"}
	req := RecoverRolloutRequest{Action: "abort", DeploymentID: candidate, ExpectedPredecessorDeploymentID: predecessor, ExpectedReview: &review, Reason: strings.Repeat("é", AuditReasonMaxBytes)}
	hash := ServiceAbortRequestHash(app, req)
	alias := req
	alias.DeploymentID = strings.ReplaceAll(candidate, "-", "")
	if ServiceAbortRequestHash(strings.ReplaceAll(app, "-", ""), alias) != hash || req.ExpectedReview.LatestDeploymentID != candidate {
		t.Fatal("UUID spelling changed request identity or hashing mutated caller data")
	}
	alias.Reason = "changed"
	if ServiceAbortRequestHash(app, alias) == hash {
		t.Fatal("reason not bound")
	}
	alias = req
	changed := review
	changed.ConfigurationHash = "changed"
	alias.ExpectedReview = &changed
	if ServiceAbortRequestHash(app, alias) == hash {
		t.Fatal("review not bound")
	}
	attempt := strings.Repeat("a", 32)
	var posts, gets int
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			posts++
			if r.Header.Get(ServiceAbortAttemptHeader) != attempt {
				t.Error("missing exact service abort identity")
			}
			_ = json.NewEncoder(w).Encode(RolloutTransitionResponse{})
		} else {
			gets++
			if r.Header.Get(ServiceAbortAttemptHeader) != "" || r.Header.Get("Idempotency-Key") != "" {
				t.Error("read leaked mutation metadata")
			}
			_ = json.NewEncoder(w).Encode(ServiceAbortReceipt{AttemptID: attempt})
		}
	}))
	defer backend.Close()
	client := NewClient(backend.URL, "token").SetCompletionCache(nil)
	ctx := ContextWithIdempotencyKey(ContextWithServiceAbortAttempt(t.Context(), attempt), "private-replay-key")
	if _, err := client.RecoverReviewedServiceRollout(ctx, "alpha", candidate, predecessor, req.Reason, review); err != nil {
		t.Fatal(err)
	}
	if _, err := client.GetServiceAbortReceipt(ctx, "alpha", attempt); err != nil {
		t.Fatal(err)
	}
	if posts != 1 || gets != 1 {
		t.Fatalf("transport calls: %d/%d", posts, gets)
	}
}
