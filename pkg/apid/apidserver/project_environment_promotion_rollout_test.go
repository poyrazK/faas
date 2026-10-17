package apidserver

import (
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/internal/promotionrollouttest"
	"github.com/onebox-faas/faas/pkg/api"
	promotionapi "github.com/onebox-faas/faas/pkg/apid/promotion"
	"github.com/onebox-faas/faas/pkg/promotionrollout"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestPromotionRolloutAdmissionAndAcceptedOperations(t *testing.T) {
	oldDelay := routeProbeChallengeDelay
	routeProbeChallengeDelay = 0
	t.Cleanup(func() { routeProbeChallengeDelay = oldDelay })
	for _, scenario := range []string{"pause_before_acceptance", "finish_after_pause", "abort_after_pause"} {
		t.Run(scenario, func(t *testing.T) {
			srv, store, account, project, app, baseline := newPromotionHealthAPIFixture(t)
			srv.WithPromotionHealthEnabled(true)
			srv.routeProbes = promotionHealthAPISender{}
			fixture := promotionrollouttest.New(t, promotionrollout.Target{AccountID: account.ID, ProjectID: project.ID})
			srv.promotionRollout = fixture.Gate
			now := time.Now().UTC()
			store.SetClockForTest(func() time.Time { return now })
			gate, err := store.GetRouteHealthGate(t.Context(), account.ID, app.ID)
			mustSnapshotPromotionAPI(t, err)
			_, err = store.SetRouteHealthGate(t.Context(), account.ID, app.ID, api.SetRouteHealthGateRequest{Mode: "report", ExpectedRevision: &gate.Revision, Routes: []api.RouteHealthRoute{{Method: "GET", Path: "/database-health", Probe: &api.RouteHealthProbe{Path: "/database-health"}}}})
			mustSnapshotPromotionAPI(t, err)
			plan, problem := srv.buildProjectEnvironmentPromotionPlan(t.Context(), account, project.Slug, "production", "staging", true, true)
			if problem != nil || !plan.Preview.CanPromote {
				t.Fatalf("preview: %v %+v", problem, plan.Preview)
			}
			body, _ := json.Marshal(api.PromoteProjectEnvironmentRequest{FromEnvironment: "production", PromotionToken: plan.Preview.PromotionToken})
			post := func(key string) *api.ProjectEnvironmentPromotionResponse {
				req, rec := projectRequest(http.MethodPost, "/promote", project.Slug, body)
				req.SetPathValue("environment", "staging")
				req.Header.Set("Idempotency-Key", key)
				srv.promoteProjectEnvironment(rec, req, account)
				if rec.Code == http.StatusAccepted || rec.Code == http.StatusOK {
					var result api.ProjectEnvironmentPromotionResponse
					mustSnapshotPromotionAPI(t, json.Unmarshal(rec.Body.Bytes(), &result))
					return &result
				}
				if rec.Code != http.StatusConflict && rec.Code != http.StatusServiceUnavailable {
					t.Fatalf("unexpected admission: %d %s", rec.Code, rec.Body)
				}
				return nil
			}
			if scenario == "pause_before_acceptance" {
				fixture.Policy.Paused = true
				fixture.WritePolicy(t)
				if post("revoked") != nil {
					t.Fatal("stale preview bypassed pause")
				}
				if _, _, err := store.ProjectEnvironmentPromotionByIdempotencyKey(t.Context(), account.ID, project.Slug, "revoked"); !errors.Is(err, state.ErrNotFound) {
					t.Fatal("rejection created an operation")
				}
				return
			}
			admitted := post("accepted")
			if admitted == nil {
				t.Fatal("qualified admission refused")
			}
			fixture.Policy.Paused = true
			fixture.WritePolicy(t)
			mustSnapshotPromotionAPI(t, os.Remove(fixture.ApprovalPath))
			if post("new-after-pause") != nil {
				t.Fatal("new admission bypassed revoked evidence")
			}
			if replay := post("accepted"); replay == nil || replay.PromotionID != admitted.PromotionID {
				t.Fatal("pause blocked durable idempotent replay")
			}
			// A replacement owner has neither boot flags nor rollout configuration.
			restarted := newServer(store, srv.log, "gregale.dev", noopNotifier{})
			restarted.routeProbes = srv.routeProbes
			job := state.PromotionHealthJob{AccountID: account.ID, ProjectSlug: project.Slug, ToEnvironment: "staging", PromotionID: admitted.PromotionID}
			if scenario == "abort_after_pause" {
				h, err := store.ProjectEnvironmentPromotionHealth(t.Context(), account.ID, admitted.PromotionID)
				mustSnapshotPromotionAPI(t, err)
				control, _ := json.Marshal(api.ProjectPromotionControlRequest{RequestID: strings.Repeat("a", 32), ExpectedGeneration: h.Generation})
				req, rec := projectRequest(http.MethodPost, "/abort", project.Slug, control)
				req.SetPathValue("environment", "staging")
				req.SetPathValue("promotion", admitted.PromotionID)
				req.SetPathValue("action", "abort")
				restarted.controlProjectPromotion(rec, req, account)
				if rec.Code != http.StatusOK {
					t.Fatalf("abort blocked: %d %s", rec.Code, rec.Body)
				}
				active, err := store.ActiveProjectReleaseSet(t.Context(), account.ID, project.ID, "staging")
				mustSnapshotPromotionAPI(t, err)
				if active.ID != baseline.ID {
					t.Fatal("early abort changed graph")
				}
				return
			}
			for range 8 {
				actions := restarted.promotionHealthActions()
				actions.Now = func() time.Time { return now }
				mustSnapshotPromotionAPI(t, promotionapi.ReconcileHealth(t.Context(), store, job, actions))
				now = now.Add(api.ProjectReleaseVerificationInterval)
			}
			final, _, err := store.ProjectEnvironmentPromotionByID(t.Context(), account.ID, project.Slug, "staging", admitted.PromotionID)
			mustSnapshotPromotionAPI(t, err)
			if final.Status != "succeeded" || final.TargetReleaseSetID == "" {
				t.Fatalf("accepted operation stranded by rollout pause: %+v", final)
			}
		})
	}
}
