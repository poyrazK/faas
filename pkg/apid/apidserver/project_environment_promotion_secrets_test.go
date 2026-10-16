package apidserver

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"filippo.io/age"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/secretbox"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestPromotionSecretsAPI(t *testing.T) {
	for _, scenario := range []string{"accepted", "disabled", "read_scope", "stale", "approval_binding", "unknown_field", "expired_versions"} {
		t.Run(scenario, func(t *testing.T) {
			srv, store, acct, project, app, sourceGraph := newPromotionHealthAPIFixture(t)
			srv.WithProjectReleaseSecretsEnabled(scenario != "disabled").WithPromotionHealthEnabled(true)
			srv.routeProbes = promotionHealthAPISender{}
			ctx := t.Context()
			mustSnapshotPromotionAPI(t, store.MarkAccountEmailVerified(ctx, acct.ID))
			gate, err := store.GetRouteHealthGate(ctx, acct.ID, app.ID)
			mustSnapshotPromotionAPI(t, err)
			_, err = store.SetRouteHealthGate(ctx, acct.ID, app.ID, api.SetRouteHealthGateRequest{Mode: "report", ExpectedRevision: &gate.Revision, Routes: []api.RouteHealthRoute{{Method: "GET", Path: "/database-health", Probe: &api.RouteHealthProbe{Path: "/database-health"}}}})
			mustSnapshotPromotionAPI(t, err)
			identity, err := age.GenerateX25519Identity()
			mustSnapshotPromotionAPI(t, err)
			cipher, err := secretbox.SealOne(identity.Recipient(), "TOKEN", "private-production", 4096)
			mustSnapshotPromotionAPI(t, err)
			mustSnapshotPromotionAPI(t, store.UpsertAppSecretWithClassInScope(ctx, acct.ID, app.ID, "production", "TOKEN", identity.Recipient().String(), strings.Repeat("e", 64), api.SecretClassPersistent, cipher))
			stage, err := store.StageProjectReleaseSecrets(ctx, acct.ID, project.ID, strings.Repeat("a", 32), strings.Repeat("b", 64), []state.ProjectReleaseSealedSecret{{Workload: "api", AppID: app.ID, Key: "TOKEN", Kid: identity.Recipient().String(), ValueHash: strings.Repeat("c", 64), Ciphertext: cipher}}, map[string]map[string]string{"api": {"TOKEN": "private-next"}})
			mustSnapshotPromotionAPI(t, err)
			key, hash, err := api.GenerateAPIKey()
			mustSnapshotPromotionAPI(t, err)
			scopes := api.ScopesAdminOnly
			if scenario == "read_scope" {
				scopes = []string{api.ScopeProjectEnvironmentRead}
			}
			_, err = store.CreateAPIKey(ctx, acct.ID, hash, "promotion", scopes)
			mustSnapshotPromotionAPI(t, err)
			_, err = store.UpdateProjectEnvironmentProtection(ctx, acct.ID, project.ID, "production", true)
			mustSnapshotPromotionAPI(t, err)
			secretHashes, err := projectEnvironmentSecretRevisionHashes(ctx, store, acct.ID, "staging", &sourceGraph, map[string]state.App{app.Slug: app})
			mustSnapshotPromotionAPI(t, err)
			workloadHashes, _, err := store.ProjectEnvironmentWorkloadConfigHashes(ctx, acct.ID, project.ID, "staging", sourceGraph.ID)
			mustSnapshotPromotionAPI(t, err)
			httpStatus := 200
			result := state.ProjectEnvironmentQualificationResult{WorkloadSlug: app.Slug, DeploymentID: sourceGraph.Members[0].DeploymentID, Status: "passed", HTTPStatus: &httpStatus}
			_, err = store.CreateProjectEnvironmentQualification(ctx, acct.ID, project.ID, "staging", sourceGraph.ID, 0, api.EmptyProjectEnvironmentConfigHash(), secretHashes, []state.ProjectEnvironmentQualificationCheck{{Name: "health", Status: "passed", Results: []state.ProjectEnvironmentQualificationResult{result}}, {Name: "smoke", Status: "passed", Results: []state.ProjectEnvironmentQualificationResult{result}}}, workloadHashes)
			mustSnapshotPromotionAPI(t, err)
			request := api.ProjectEnvironmentPromotionSecretPreviewRequest{FromEnvironment: "staging", SyncConfig: true, Verify: true, Secrets: stage.Versions}
			if scenario == "expired_versions" {
				request.Secrets = map[string]map[string]string{"api": {"TOKEN": "eeeeeeee-eeee-4eee-8eee-eeeeeeeeeeee"}}
			}
			raw, _ := json.Marshal(request)
			if scenario == "unknown_field" {
				raw = append(raw[:len(raw)-1], []byte(`,"values":{"TOKEN":"private-next"}}`)...)
			}
			call := func(path string, body []byte, idem string) *httptest.ResponseRecorder {
				r := httptest.NewRequest(http.MethodPost, "/v1/projects/shop/environments/production/"+path, bytes.NewReader(body))
				r.Header.Set("Authorization", "Bearer "+key)
				r.Header.Set("Content-Type", "application/json")
				if idem != "" {
					r.Header.Set("Idempotency-Key", idem)
				}
				w := httptest.NewRecorder()
				srv.handler().ServeHTTP(w, r)
				return w
			}
			response := call("promotion-preview", raw, "")
			want := map[string]int{"disabled": 503, "read_scope": 403, "unknown_field": 400}[scenario]
			if want != 0 {
				if response.Code != want {
					t.Fatalf("guard=%d want=%d %s", response.Code, want, response.Body)
				}
				return
			}
			if response.Code != 200 {
				t.Fatalf("preview=%d %s", response.Code, response.Body)
			}
			var preview api.ProjectEnvironmentPromotionPreviewResponse
			mustSnapshotPromotionAPI(t, json.Unmarshal(response.Body.Bytes(), &preview))
			if scenario == "expired_versions" {
				if preview.CanPromote || !strings.Contains(strings.Join(preview.BlockingReasons, ";"), "production secret versions are missing, expired") {
					t.Fatal("expired staging has no actionable blocker")
				}
				return
			}
			if !preview.CanPromote || preview.Secrets == nil || len(preview.Changes[0].SecretChanges) != 1 {
				t.Fatalf("secret preview incomplete: %s", response.Body)
			}
			approvalRaw, _ := json.Marshal(api.CreateProjectEnvironmentApprovalRequest{PromotionToken: preview.PromotionToken})
			response = call("approvals", approvalRaw, "approve-secret")
			if response.Code != 201 {
				t.Fatalf("approval=%d %s", response.Code, response.Body)
			}
			var approval api.ProjectEnvironmentApprovalResponse
			mustSnapshotPromotionAPI(t, json.Unmarshal(response.Body.Bytes(), &approval))
			if scenario == "stale" {
				mustSnapshotPromotionAPI(t, store.UpsertAppEnvInScope(ctx, acct.ID, app.ID, "production", "MODE", "operator-edit"))
			}
			if scenario == "approval_binding" {
				request.Secrets = map[string]map[string]string{"api": {}}
				raw, _ = json.Marshal(request)
				response = call("promotion-preview", raw, "")
				mustSnapshotPromotionAPI(t, json.Unmarshal(response.Body.Bytes(), &preview))
			}
			body, _ := json.Marshal(api.PromoteProjectEnvironmentRequest{FromEnvironment: "staging", PromotionToken: preview.PromotionToken, ApprovalToken: approval.ApprovalToken})
			response = call("promote", body, "promote-secret")
			want = http.StatusAccepted
			if scenario == "stale" || scenario == "approval_binding" {
				want = http.StatusConflict
			}
			if response.Code != want {
				t.Fatalf("admission=%d want=%d %s", response.Code, want, response.Body)
			}
			if strings.Contains(response.Body.String(), "private-") {
				t.Fatal("credential in response")
			}
		})
	}
}
