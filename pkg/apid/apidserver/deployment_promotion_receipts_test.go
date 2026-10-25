// adr: 20261025090000
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

func TestDeploymentPromotionReceiptHTTP(t *testing.T) {
	e := apidserver.Setup(t, api.PlanPro)
	s, a := *e.Store(), *e.Acct()
	app, err := s.CreateApp(t.Context(), state.App{AccountID: a.ID, Slug: "promotion-receipts", Type: state.AppTypeApp, RAMMB: 256})
	if err != nil {
		t.Fatal(err)
	}
	create := func(percent int) state.Deployment {
		t.Helper()
		d, err := s.CreateDeployment(t.Context(), state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage, ImageDigest: "sha256:" + strings.Repeat("a", 64), TrafficPercent: percent, TrafficPercentExplicit: true})
		if err != nil {
			t.Fatal(err)
		}
		if err = s.MarkDeploymentLive(t.Context(), d.ID); err != nil {
			t.Fatal(err)
		}
		return d
	}
	base, candidate := create(100), create(0)
	attempt := strings.Repeat("c", 32)
	headers := map[string]string{api.DeploymentPromotionAttemptHeader: attempt}
	path := "/v1/deployments/" + candidate.ID + "/traffic"
	req := api.UpdateDeploymentTrafficRequest{TrafficPercent: 100, ExpectedServingDeploymentID: &base.ID}
	for _, id := range []string{"bad", strings.Repeat("A", 32)} {
		if got := e.Do(t, http.MethodPatch, path, req, map[string]string{api.DeploymentPromotionAttemptHeader: id}); got.Code != 400 {
			t.Fatalf("invalid header=%d %s", got.Code, got.Body)
		}
	}
	handler := *e.H()
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPatch {
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
	if _, err = client.PatchDeploymentTrafficIfServing(api.ContextWithDeploymentPromotionAttempt(t.Context(), attempt), candidate.ID, 100, base.ID); err == nil {
		t.Fatal("discarded native reply unexpectedly succeeded")
	}

	reader, hash, err := api.GenerateAPIKey()
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.CreateAPIKey(t.Context(), a.ID, hash, "receipt-reader", []string{api.ScopeAppsRead}); err != nil {
		t.Fatal(err)
	}
	lookup := "/v1/apps/promotion-receipts/deployment-promotions/" + attempt
	got := e.DoAs(t, http.MethodGet, lookup, nil, nil, reader)
	var receipt api.DeploymentPromotionReceipt
	expected, _ := api.DeploymentPromotionRequestHash(candidate.ID, req)
	if got.Code != 200 || json.Unmarshal(got.Body.Bytes(), &receipt) != nil || receipt.RequestHash != expected || receipt.DeploymentID != candidate.ID {
		t.Fatalf("read receipt=%d %s", got.Code, got.Body)
	}
	if _, err = s.UpdateDeploymentTraffic(t.Context(), base.ID, 100, candidate.ID); err != nil {
		t.Fatal(err)
	}
	got = e.Do(t, http.MethodPatch, path, req, headers)
	if got.Code != 409 || !strings.Contains(got.Body.String(), "deployment_promotion_attempt_accepted") {
		t.Fatalf("accepted retry=%d %s", got.Code, got.Body)
	}
	changed := req
	empty := ""
	changed.ExpectedServingDeploymentID = &empty
	got = e.Do(t, http.MethodPatch, path, changed, headers)
	if got.Code != 409 || !strings.Contains(got.Body.String(), "deployment_promotion_attempt_conflict") {
		t.Fatalf("changed retry=%d %s", got.Code, got.Body)
	}
	current, _ := s.DeploymentByID(t.Context(), base.ID)
	if current.TrafficPercent != 100 {
		t.Fatal("old retry changed traffic")
	}
	if got = e.DoAs(t, http.MethodGet, lookup, nil, nil, reader); got.Code != 200 {
		t.Fatal("history disappeared after later traffic")
	}
	if got = e.DoAs(t, http.MethodGet, "/v1/apps/unknown/deployment-promotions/"+attempt, nil, nil, reader); got.Code != 404 {
		t.Fatal("foreign target exposed receipt")
	}
	if got = e.DoAs(t, http.MethodGet, "/v1/apps/promotion-receipts/deployment-promotions/bad", nil, nil, reader); got.Code != 400 {
		t.Fatal("malformed lookup accepted")
	}
}

type discardPromotionReply struct{ http.ResponseWriter }

func (discardPromotionReply) WriteHeader(int)                {}
func (discardPromotionReply) Write(body []byte) (int, error) { return len(body), nil }
