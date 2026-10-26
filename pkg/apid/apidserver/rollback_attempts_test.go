// adr: 20261026090000
package apidserver_test

import (
	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestCheckedRollbackAttemptHTTP(t *testing.T) {
	e, app, target, current := apidserver.CheckedRollbackAPIFixture(t)
	req := api.RollbackRequest{TargetDeploymentID: &target.ID, ExpectedCurrentDeploymentID: &current.ID, Reason: "original"}
	path := "/v1/apps/" + app.Slug + "/rollback"
	attempt := strings.ReplaceAll(uuid.NewString(), "-", "")
	operationID := uuid.MustParse(attempt).String()
	headers := map[string]string{api.RollbackAttemptHeader: attempt}
	for _, bad := range []string{"bad", strings.Repeat("A", 32)} {
		if got := e.Do(t, "POST", path, req, map[string]string{api.RollbackAttemptHeader: bad}); got.Code != 400 {
			t.Fatalf("invalid header: %d %s", got.Code, got.Body)
		}
	}
	if got := e.Do(t, "POST", path, api.RollbackRequest{}, headers); got.Code != 400 {
		t.Fatal("unguarded attempt accepted")
	}
	acknowledged := req
	acknowledged.AcknowledgeReleases = true
	if got := e.Do(t, "POST", path, acknowledged, headers); got.Code != 400 {
		t.Fatal("acknowledged request accepted")
	}
	handler := *e.H()
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "POST" {
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
	if _, err := client.CheckedRollback(api.ContextWithRollbackAttempt(t.Context(), attempt), app.Slug, req); err == nil {
		t.Fatal("lost native network reply succeeded")
	}
	read, err := client.GetRollbackOperation(t.Context(), app.Slug, operationID)
	if err != nil || read.ID != operationID || read.TargetDeploymentID != target.ID || read.Status != "preparing" {
		t.Fatalf("accepted receipt: %+v %v", read, err)
	}
	store := *e.Store()
	got, _ := store.DeploymentByID(t.Context(), current.ID)
	if got.TrafficPercent != 100 {
		t.Fatal("acceptance was treated as completion")
	}
	if err = store.UpdateCheckedRollback(t.Context(), read, "failed", "later_failure", nil); err != nil {
		t.Fatal(err)
	}
	replay := e.Do(t, "POST", path, req, headers)
	if replay.Code != 409 || !strings.Contains(replay.Body.String(), "rollback_attempt_accepted") {
		t.Fatalf("accepted attempt replay: %d %s", replay.Code, replay.Body)
	}
	compact := req
	compactTarget, compactCurrent := uuid.MustParse(target.ID).String(), uuid.MustParse(current.ID).String()
	compact.TargetDeploymentID, compact.ExpectedCurrentDeploymentID = &compactTarget, &compactCurrent
	if got := e.Do(t, "POST", path, compact, headers); got.Code != 409 || !strings.Contains(got.Body.String(), "rollback_attempt_accepted") {
		t.Fatalf("equivalent UUID spelling changed accepted identity: %d %s", got.Code, got.Body)
	}
	changed := req
	changed.Reason = "changed"
	replay = e.Do(t, "POST", path, changed, headers)
	if replay.Code != 409 || !strings.Contains(replay.Body.String(), "rollback_attempt_conflict") {
		t.Fatalf("changed attempt: %d %s", replay.Code, replay.Body)
	}
	reader, hash, err := api.GenerateAPIKey()
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.CreateAPIKey(t.Context(), e.Acct().ID, hash, "receipt-reader", []string{api.ScopeAppsRead}); err != nil {
		t.Fatal(err)
	}
	lookup := "/v1/apps/" + app.Slug + "/rollbacks/" + operationID
	if got := e.DoAs(t, "GET", lookup, nil, nil, reader); got.Code != 200 {
		t.Fatalf("readonly receipt: %d %s", got.Code, got.Body)
	}
	if got := e.DoAs(t, "GET", "/v1/apps/foreign/rollbacks/"+operationID, nil, nil, reader); got.Code != 404 {
		t.Fatal("foreign app receipt exposed")
	}
}
