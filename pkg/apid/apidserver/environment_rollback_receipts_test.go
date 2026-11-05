// adr: 20261105102920
package apidserver_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestEnvironmentRollbackReceiptHTTPLostReply(t *testing.T) {
	srv, store, acct, project, _ := apidserver.NewProjectLifecycleFixture(t)
	ctx := t.Context()
	from, err := store.CreateProjectEnvironment(ctx, state.ProjectEnvironment{AccountID: acct.ID, ProjectID: project.ID, Slug: "staging"})
	if err != nil {
		t.Fatal(err)
	}
	target, err := store.ProjectEnvironmentBySlug(ctx, acct.ID, project.ID, "production")
	if err != nil {
		t.Fatal(err)
	}
	promotion, _, err := store.CreateProjectEnvironmentPromotion(ctx, state.ProjectEnvironmentPromotion{AccountID: acct.ID, ProjectID: project.ID, ProjectSlug: project.Slug, FromEnvironment: from.Slug, ToEnvironment: target.Slug, PromotionHash: strings.Repeat("a", 64), IdempotencyKey: "promotion", Status: "succeeded"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	attempt := strings.Repeat("a", 32)
	hash := api.EnvironmentRollbackRequestHash(project.ID, from.ID, target.ID, promotion.ID, promotion.PromotionHash)
	path := "/v1/projects/" + project.Slug + "/environments/production/promotions/" + promotion.ID + "/rollback"
	request := func(headers map[string]string) *httptest.ResponseRecorder {
		req, rec := apidserver.ProjectRequest("POST", path, project.Slug, nil)
		req.SetPathValue("environment", target.Slug)
		req.SetPathValue("promotion", promotion.ID)
		req.Header.Set("Idempotency-Key", "original")
		for k, v := range headers {
			req.Header.Set(k, v)
		}
		srv.RollbackProjectEnvironmentPromotion(rec, req, acct)
		return rec
	}
	for _, headers := range []map[string]string{{api.EnvironmentRollbackAttemptHeader: attempt}, {api.EnvironmentRollbackHashHeader: hash}, {api.EnvironmentRollbackAttemptHeader: strings.Repeat("A", 32), api.EnvironmentRollbackHashHeader: hash}, {api.EnvironmentRollbackAttemptHeader: attempt, api.EnvironmentRollbackHashHeader: "bad"}} {
		if rec := request(headers); rec.Code != 400 {
			t.Fatalf("malformed header: %d %s", rec.Code, rec.Body)
		}
	}
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.SetPathValue("slug", project.Slug)
		r.SetPathValue("environment", target.Slug)
		r.SetPathValue("promotion", promotion.ID)
		if r.Method == "POST" {
			srv.RollbackProjectEnvironmentPromotion(discardPromotionReply{w}, r, acct)
			conn, _, err := w.(http.Hijacker).Hijack()
			if err != nil {
				t.Error(err)
				return
			}
			_ = conn.Close()
			return
		}
		r.SetPathValue("attempt_id", r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:])
		srv.GetEnvironmentRollbackReceipt(w, r, acct)
	}))
	defer backend.Close()
	client := api.NewClient(backend.URL, "fixture-token")
	write := api.ContextWithEnvironmentRollbackAttempt(api.ContextWithIdempotencyKey(ctx, "original"), attempt, hash)
	if _, err := client.RollbackProjectEnvironmentPromotion(write, project.Slug, target.Slug, promotion.ID); err == nil {
		t.Fatal("lost native reply succeeded")
	}
	receipt, err := client.GetEnvironmentRollbackReceipt(write, project.Slug, target.Slug, promotion.ID, attempt)
	if err != nil || receipt.RequestHash != hash || receipt.EnvironmentID != target.ID || receipt.FromEnvironmentID != from.ID || receipt.AcceptedAt.IsZero() {
		t.Fatalf("receipt=%+v %v", receipt, err)
	}
	if _, err = store.UpdateProjectEnvironmentPromotionRollback(ctx, acct.ID, promotion.ID, "rollback_failed", "later failure", nil); err != nil {
		t.Fatal(err)
	}
	headers := map[string]string{api.EnvironmentRollbackAttemptHeader: attempt, api.EnvironmentRollbackHashHeader: hash}
	if got := request(headers); got.Code != 409 || !strings.Contains(got.Body.String(), "environment_rollback_attempt_accepted") {
		t.Fatalf("terminal replay: %d %s", got.Code, got.Body)
	}
	headers[api.EnvironmentRollbackHashHeader] = strings.Repeat("b", 64)
	if got := request(headers); got.Code != 409 || !strings.Contains(got.Body.String(), "environment_rollback_attempt_conflict") {
		t.Fatalf("changed original: %d %s", got.Code, got.Body)
	}
	encoded, _ := json.Marshal(receipt)
	if strings.Contains(string(encoded), "original") || strings.Contains(string(encoded), "rollback_failed") {
		t.Fatal("receipt exposed retry credential or progress")
	}
}
