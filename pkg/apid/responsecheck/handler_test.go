package responsecheck

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// Keep a named store and only the three domain reads, without generating
// wrappers for the full state.Store surface.
type changingStore struct {
	mem     *state.MemStore
	changed atomic.Bool
}

func (s *changingStore) AppByID(ctx context.Context, id string) (state.App, error) {
	return s.mem.AppByID(ctx, id)
}
func (s *changingStore) DeploymentByID(ctx context.Context, id string) (state.Deployment, error) {
	return s.mem.DeploymentByID(ctx, id)
}
func (s *changingStore) ProjectEnvironmentBySlug(ctx context.Context, account, project, slug string) (state.ProjectEnvironment, error) {
	env, err := s.mem.ProjectEnvironmentBySlug(ctx, account, project, slug)
	if s.changed.Load() {
		env.ID = "018f0000-0000-7000-8000-ffffffffffff"
	}
	return env, err
}

func TestDeploymentResponseOwnerAndTargetContract(t *testing.T) {
	for _, outcome := range []string{"success", "guest denial", "foreign app", "wrong scope", "wrong environment", "environment changed during check", "wrong response deployment", "oversized response", "redirect", "unknown field"} {
		t.Run(outcome, func(t *testing.T) {
			ctx := t.Context()
			mem := state.NewMemStore()
			store := &changingStore{mem: mem}
			account, err := mem.CreateAccount(ctx, "response@example.test", api.PlanPro)
			if err != nil {
				t.Fatal(err)
			}
			project, err := mem.CreateProject(ctx, state.Project{AccountID: account.ID, Slug: "response"})
			if err != nil {
				t.Fatal(err)
			}
			env, err := mem.CreateProjectEnvironment(ctx, state.ProjectEnvironment{AccountID: account.ID, ProjectID: project.ID, Slug: "staging"})
			if err != nil {
				t.Fatal(err)
			}
			app, err := mem.CreateApp(ctx, state.App{AccountID: account.ID, ProjectID: project.ID, Slug: "response-api", Type: state.AppTypeApp})
			if err != nil {
				t.Fatal(err)
			}
			dep, err := mem.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Scope: "staging", Status: state.DeployLive})
			if err != nil {
				t.Fatal(err)
			}
			request := api.DeploymentResponseCheckRequest{AppID: app.ID, ProjectID: project.ID, EnvironmentID: env.ID, Scope: "staging", Path: "/ready?ephemeral=value"}
			calls := 0
			gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				if r.Method != http.MethodPost || r.URL.Path != "/v1/internal/apps/response-api/response-check" || r.URL.Query().Get("deployment_id") != dep.ID || r.Header.Get("Authorization") != "" || r.Header.Get("Cookie") != "" {
					t.Error("gateway received the wrong route or account credentials")
				}
				var received api.DeploymentResponseCheckRequest
				if json.NewDecoder(r.Body).Decode(&received) != nil || received != request {
					t.Error("gateway lost the pinned request")
				}
				response := api.DeploymentResponseCheckResult{AppID: app.ID, DeploymentID: dep.ID, Scope: "staging", ProjectID: project.ID, EnvironmentID: env.ID, InstanceID: uuid.NewString(), HTTPStatus: 200, Body: "ready"}
				switch outcome {
				case "guest denial":
					response.HTTPStatus = 403
				case "environment changed during check":
					store.changed.Store(true)
				case "wrong response deployment":
					response.DeploymentID = uuid.NewString()
				case "oversized response":
					response.Body = strings.Repeat("x", api.DeploymentResponseCheckMaxBodyBytes+1)
				case "redirect":
					http.Redirect(w, r, "http://must-not-contact.example.test/", http.StatusFound)
					return
				}
				_ = json.NewEncoder(w).Encode(response)
			}))
			defer gateway.Close()
			service := Service{Store: store, GatewayURL: gateway.URL, LoadApp: func(http.ResponseWriter, *http.Request, state.Account, string) (state.App, bool) { return app, true }}
			switch outcome {
			case "foreign app":
				request.AppID = uuid.NewString()
			case "wrong scope":
				request.Scope = "production"
			case "wrong environment":
				request.EnvironmentID = uuid.NewString()
			}
			body, _ := json.Marshal(request)
			if outcome == "unknown field" {
				body = append(body[:len(body)-1], []byte(`,"headers":{"Authorization":"secret"}}`)...)
			}
			r := httptest.NewRequest(http.MethodPost, "/", bytes.NewReader(body))
			r.SetPathValue("slug", app.Slug)
			r.SetPathValue("deployment", dep.ID)
			r.Header.Set("Authorization", "Bearer owner-secret")
			r.Header.Set("Cookie", "session=private")
			w := httptest.NewRecorder()
			service.Check(w, r, account)
			wantSuccess := outcome == "success" || outcome == "guest denial"
			if (w.Code == http.StatusOK) != wantSuccess || w.Header().Get("Cache-Control") != "no-store" {
				t.Fatalf("code=%d body=%s", w.Code, w.Body)
			}
			if wantSuccess {
				var result api.DeploymentResponseCheckResult
				if json.Unmarshal(w.Body.Bytes(), &result) != nil || !result.Matches(request, dep.ID) {
					t.Fatalf("invalid evidence: %s", w.Body)
				}
			}
			if outcome == "foreign app" || outcome == "wrong scope" || outcome == "wrong environment" || outcome == "unknown field" {
				if calls != 0 {
					t.Fatal("invalid selection reached the gateway")
				}
			} else if calls != 1 {
				t.Fatal("a check was retried or never sent")
			}
			if !wantSuccess && strings.Contains(w.Body.String(), `"body":"ready"`) {
				t.Fatal("unverified response content escaped")
			}
			invocations, err := mem.ListInvocationsForApp(ctx, app.ID)
			if err != nil || len(invocations) != 0 {
				t.Fatal("response check created durable invocation work")
			}
		})
	}
}
