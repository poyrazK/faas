// adr: 20261111114059
package apidserver_test

import (
	"errors"
	"net/http/httptest"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

// A source deploy applies its gregale.yaml deployment settings itself, so
// an agent's source deploy that adds warm replicas is priced like the same
// PATCH: on a zero budget it waits for a person's budget approval and writes
// none of the settings. A source with no standing cost deploys, and a
// person's key is never charged.
func TestAgentBudgetSourceDeploySettings(t *testing.T) {
	const service = "lifecycle:\n  execution_mode: service\n  restart_policy: always\n  service_replicas:\n    min: 1\n    max: 2\n    desired: 2\n"
	for _, transport := range []string{"multipart", "source-tarball", "source-ref", "resumable"} {
		t.Run(transport, func(t *testing.T) {
			f := newSourceAdmissionFixture(t)
			store := *f.env.Store()
			app, err := store.AppByID(t.Context(), *f.env.AppID())
			if err != nil {
				t.Fatal(err)
			}
			srv := httptest.NewServer(*f.env.H())
			t.Cleanup(srv.Close)
			plaintext := zeroBudgetAgent(t, store, app.AccountID, api.ScopeAppsRead, api.ScopeDeployWrite)
			agent := sourceAdmissionFixture{env: f.env, client: api.NewClient(srv.URL, plaintext).SetCompletionCache(nil)}

			_, err = agent.deploy(t, transport, sourceAdmissionArchive(t, service), api.DeployAnnotations{})
			var apiErr *api.APIError
			if !errors.As(err, &apiErr) || apiErr.Problem.AgentApproval == nil || apiErr.Problem.AgentApproval.Category != api.AgentApprovalBudget {
				t.Fatalf("an agent's service deploy on a zero budget = %v; want a budget approval", err)
			}
			if got, err := store.AppByID(t.Context(), app.ID); err != nil || got.Manifest.ExecutionMode == api.ExecutionModeService {
				t.Fatalf("the refused deploy changed the app: mode %q, %v", got.Manifest.ExecutionMode, err)
			}

			if _, err := agent.deploy(t, transport, sourceAdmissionArchive(t, "lifecycle:\n  request_timeout_s: 20\n"), api.DeployAnnotations{}); err != nil {
				t.Fatalf("a source with no standing cost = %v", err)
			}
			if _, err := f.deploy(t, transport, sourceAdmissionArchive(t, service), api.DeployAnnotations{}); err != nil {
				t.Fatalf("a person's service deploy = %v", err)
			}
		})
	}
}
