// adr: 20261104144000
package apidserver_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
)

func TestServiceAbortReceiptLostNativeReply(t *testing.T) {
	e, app, predecessor, candidate := apidserver.PromotionFixture(t)
	s := *e.Store()
	if err := s.SetDeploymentCanaryState(t.Context(), candidate.ID, "none", 0, 0, time.Now(), "rolling_out"); err != nil {
		t.Fatal(err)
	}
	review, err := s.ReadReleaseReview(t.Context(), app.ID, "default")
	if err != nil {
		t.Fatal(err)
	}
	// Python's generated UUID fields round-trip compact native IDs as dashed.
	review.AppID = uuid.MustParse(review.AppID).String()
	review.LatestDeploymentID = uuid.MustParse(review.LatestDeploymentID).String()
	for i, id := range review.ServingDeploymentIDs {
		review.ServingDeploymentIDs[i] = uuid.MustParse(id).String()
	}
	attempt := strings.Repeat("d", 32)
	body := api.RecoverRolloutRequest{Action: "abort", Reason: "native reply lost", DeploymentID: candidate.ID, ExpectedPredecessorDeploymentID: predecessor.ID, ExpectedReview: &review}
	path := "/v1/apps/" + app.Slug + "/rollouts/recover"
	headers := map[string]string{api.ServiceAbortAttemptHeader: attempt}
	for _, bad := range []string{"bad", strings.Repeat("A", 32)} {
		if got := e.Do(t, http.MethodPost, path, body, map[string]string{api.ServiceAbortAttemptHeader: bad}); got.Code != 400 {
			t.Fatalf("invalid header: %d %s", got.Code, got.Body)
		}
	}
	handler := *e.H()
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			handler.ServeHTTP(discardPromotionReply{w}, r)
			conn, _, err := w.(http.Hijacker).Hijack()
			if err != nil {
				t.Error(err)
				return
			}
			_ = conn.Close()
			return
		}
		handler.ServeHTTP(w, r)
	}))
	defer backend.Close()
	client := api.NewClient(backend.URL, *e.Key()).SetCompletionCache(nil)
	if _, err := client.RecoverReviewedServiceRollout(api.ContextWithServiceAbortAttempt(t.Context(), attempt), app.Slug, candidate.ID, predecessor.ID, body.Reason, review); err == nil {
		t.Fatal("discarded response succeeded")
	}
	receipt, err := client.GetServiceAbortReceipt(t.Context(), app.Slug, attempt)
	if err != nil || receipt.RequestHash != api.ServiceAbortRequestHash(app.ID, body) || receipt.DeploymentID != uuid.MustParse(candidate.ID).String() || receipt.PredecessorDeploymentID != uuid.MustParse(predecessor.ID).String() || receipt.RequestID == "" {
		t.Fatalf("native receipt: %+v %v", receipt, err)
	}
	if err := s.SetDeploymentCanaryState(t.Context(), candidate.ID, "none", 0, 0, time.Now(), "aborted"); err != nil {
		t.Fatal(err)
	}
	if err := s.UpsertAppEnv(t.Context(), (*e.Acct()).ID, app.ID, "CHANGED_AFTER_ABORT", "changed"); err != nil {
		t.Fatal(err)
	}
	if got := e.Do(t, http.MethodPost, path, body, headers); got.Code != 409 || !strings.Contains(got.Body.String(), "service_abort_attempt_accepted") {
		t.Fatalf("accepted retry: %d %s", got.Code, got.Body)
	}
	body.Reason = "changed"
	if got := e.Do(t, http.MethodPost, path, body, headers); got.Code != 409 || !strings.Contains(got.Body.String(), "service_abort_attempt_conflict") {
		t.Fatalf("conflicting retry: %d %s", got.Code, got.Body)
	}
	reader, hash, err := api.GenerateAPIKey()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateAPIKey(t.Context(), (*e.Acct()).ID, hash, "abort-reader", []string{api.ScopeAppsRead}); err != nil {
		t.Fatal(err)
	}
	lookup := "/v1/apps/" + app.Slug + "/service-aborts/" + attempt
	response := e.DoAs(t, http.MethodGet, lookup, nil, nil, reader)
	var retained api.ServiceAbortReceipt
	if response.Code != 200 || json.Unmarshal(response.Body.Bytes(), &retained) != nil || retained != receipt {
		t.Fatalf("retained read: %d %s", response.Code, response.Body)
	}
	if got := e.DoAs(t, http.MethodGet, "/v1/apps/unknown/service-aborts/"+attempt, nil, nil, reader); got.Code != 404 {
		t.Fatal("unknown app exposed evidence")
	}
	if got := e.DoAs(t, http.MethodGet, "/v1/apps/"+app.Slug+"/service-aborts/bad", nil, nil, reader); got.Code != 400 {
		t.Fatal("malformed receipt lookup accepted")
	}
	current, _ := s.DeploymentByID(t.Context(), candidate.ID)
	if current.ServiceRolloutHandoff.BindingsCheck.RequestID != receipt.RequestID {
		t.Fatal("duplicate restarted native handoff")
	}
}
