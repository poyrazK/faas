// adr: 20261105153007
package apidserver_test

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/state"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestEnvironmentPromotionReceiptHTTPLostReply(t *testing.T) {
	srv, store, acct, project, app := apidserver.NewProjectLifecycleFixture(t)
	ctx := context.Background()
	from, err := store.CreateProjectEnvironment(ctx, state.ProjectEnvironment{AccountID: acct.ID, ProjectID: project.ID, Slug: "staging"})
	if err != nil {
		t.Fatal(err)
	}
	target, err := store.ProjectEnvironmentBySlug(ctx, acct.ID, project.ID, "production")
	if err != nil {
		t.Fatal(err)
	}
	for _, scope := range []string{"staging", "production"} {
		d, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Scope: scope, SourceSHA256: "source-" + scope, Status: state.DeployPending})
		if err != nil {
			t.Fatal(err)
		}
		if err = store.SetDeploymentRootfs(ctx, d.ID, "/rootfs/"+scope, "apps/"+scope+".ext4", 42); err != nil {
			t.Fatal(err)
		}
		if err = store.MarkDeploymentLive(ctx, d.ID); err != nil {
			t.Fatal(err)
		}
	}
	req, rec := apidserver.ProjectRequest("GET", "/v1/projects/shop/environments/production/promotion-preview?from=staging", project.Slug, nil)
	req.SetPathValue("environment", target.Slug)
	srv.PreviewProjectEnvironmentPromotion(rec, req, acct)
	var preview api.ProjectEnvironmentPromotionPreviewResponse
	if rec.Code != 200 || json.Unmarshal(rec.Body.Bytes(), &preview) != nil {
		t.Fatal(rec.Code, rec.Body.String())
	}
	attempt := strings.Repeat("a", 32)
	hash := api.EnvironmentPromotionRequestHash(project.ID, from.ID, target.ID, preview.PromotionHash)
	body := api.PromoteProjectEnvironmentRequest{FromEnvironment: from.Slug, PromotionToken: preview.PromotionToken}
	path := "/v1/projects/" + project.Slug + "/environments/production/promote"
	request := func(body api.PromoteProjectEnvironmentRequest, attemptValue, hashValue string) *httptest.ResponseRecorder {
		payload, _ := json.Marshal(body)
		q, w := apidserver.ProjectRequest("POST", path, project.Slug, payload)
		q.SetPathValue("environment", target.Slug)
		q.Header.Set("Idempotency-Key", "original")
		if attemptValue != "" {
			q.Header.Set(api.EnvironmentPromotionAttemptHeader, attemptValue)
		}
		if hashValue != "" {
			q.Header.Set(api.EnvironmentPromotionHashHeader, hashValue)
		}
		srv.PromoteProjectEnvironment(w, q, acct)
		return w
	}
	for _, pair := range [][2]string{{attempt, ""}, {"", hash}, {strings.Repeat("A", 32), hash}} {
		if w := request(body, pair[0], pair[1]); w.Code != 400 {
			t.Fatal("malformed", w.Code, w.Body.String())
		}
	}
	if w := request(body, attempt, hash); w.Code != 409 {
		t.Fatal("bypassed approval", w.Code, w.Body.String())
	}
	if _, err = store.EnvironmentPromotionReceipt(ctx, acct.ID, project.Slug, target.Slug, attempt); !errors.Is(err, state.ErrNotFound) {
		t.Fatal("unapproved admission left receipt", err)
	}
	approval, _, problem := srv.IssueProjectEnvironmentPromotionApproval(ctx, acct, project.Slug, target.Slug, preview.PromotionToken)
	if problem != nil {
		t.Fatal(problem)
	}
	body.ApprovalToken = approval
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.SetPathValue("slug", project.Slug)
		r.SetPathValue("environment", target.Slug)
		if r.Method == "POST" {
			srv.PromoteProjectEnvironment(discardPromotionReply{w}, r, acct)
			conn, _, err := w.(http.Hijacker).Hijack()
			if err != nil {
				t.Error(err)
				return
			}
			_ = conn.Close()
			return
		}
		r.SetPathValue("attempt_id", r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:])
		srv.GetEnvironmentPromotionReceipt(w, r, acct)
	}))
	defer backend.Close()
	client := api.NewClient(backend.URL, "fixture")
	write := api.ContextWithEnvironmentPromotionAttempt(api.ContextWithIdempotencyKey(ctx, "original"), attempt, hash)
	if _, err = client.PromoteProjectEnvironment(write, project.Slug, target.Slug, body); err == nil {
		t.Fatal("lost reply succeeded")
	}
	receipt, err := client.GetEnvironmentPromotionReceipt(write, project.Slug, target.Slug, attempt)
	if err != nil || receipt.PromotionID == "" || receipt.RequestHash != hash || receipt.EnvironmentID != target.ID || receipt.AcceptedAt.IsZero() {
		t.Fatalf("receipt=%+v err=%v", receipt, err)
	}
	if _, err = store.UpdateProjectEnvironmentPromotion(ctx, acct.ID, receipt.PromotionID, "failed", "later failure", nil); err != nil {
		t.Fatal(err)
	}
	if w := request(body, attempt, hash); w.Code != 409 || !strings.Contains(w.Body.String(), "environment_promotion_attempt_accepted") {
		t.Fatal("re-admitted", w.Code, w.Body.String())
	}
	if w := request(body, strings.Repeat("b", 32), hash); w.Code != 409 || !strings.Contains(w.Body.String(), "environment_promotion_attempt_conflict") {
		t.Fatal("replacement", w.Code, w.Body.String())
	}
}
