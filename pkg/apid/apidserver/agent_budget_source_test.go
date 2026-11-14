// adr: 20261111114059
package apidserver_test

import (
	"bytes"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
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

// A budget preview of a Git source deploy fetches the ref and prices the
// warm capacity its gregale.yaml would add, writing nothing: no deployment,
// no settings, no approval and no spend. Other source transports still
// refuse the header.
func TestAgentBudgetSourceRefPreview(t *testing.T) {
	const service = "lifecycle:\n  execution_mode: service\n  restart_policy: always\n  service_replicas:\n    min: 1\n    max: 2\n    desired: 2\n"
	f := newSourceAdmissionFixture(t)
	store := *f.env.Store()
	ctx := t.Context()
	app, err := store.AppByID(ctx, *f.env.AppID())
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(*f.env.H())
	t.Cleanup(srv.Close)
	token := zeroBudgetAgent(t, store, app.AccountID, api.ScopeAppsRead, api.ScopeDeployWrite)
	client := api.NewClient(srv.URL, token).SetCompletionCache(nil)
	ref := api.SourceRefDeployRequest{Repo: "onebox-faas/hello", Ref: "0123456789abcdef0123456789abcdef01234567"}
	preview := func(t *testing.T, manifest string) api.AgentBudgetPreview {
		t.Helper()
		*(*f.env.Gh()).StreamBody() = apidserver.NopReadCloser{Reader: bytes.NewReader(sourceAdmissionArchive(t, manifest))}
		out, err := client.PreviewDeployFromSourceRef(ctx, app.Slug, ref)
		if err != nil || !out.Preview {
			t.Fatalf("preview = %+v, %v", out, err)
		}
		return out
	}
	unchanged := func(t *testing.T) {
		t.Helper()
		if got, err := store.AppByID(ctx, app.ID); err != nil || got.Manifest.ExecutionMode == api.ExecutionModeService {
			t.Fatalf("a preview changed the app: mode %q, %v", got.Manifest.ExecutionMode, err)
		}
		if deps, err := store.ListDeploymentsForApp(ctx, app.ID, 10, 0); err != nil || len(deps) != 0 {
			t.Fatalf("deployments after a preview = %d, %v", len(deps), err)
		}
		keys, err := store.ListAPIKeys(ctx, app.AccountID)
		if err != nil {
			t.Fatal(err)
		}
		for _, key := range keys {
			approvals, err := store.ListAgentApprovalsForKey(ctx, key.ID, 10)
			spent, spendErr := store.AgentBudgetSpent(ctx, key.ID, time.Now())
			if err != nil || spendErr != nil || len(approvals) != 0 || spent != 0 {
				t.Fatalf("key %s after a preview: approvals %d (%v), spend %d (%v)", key.ID, len(approvals), err, spent, spendErr)
			}
		}
	}

	out := preview(t, service)
	if !out.Charged || out.AddedCents <= 0 || !out.NeedsApproval || out.ApprovalCategory != api.AgentApprovalBudget {
		t.Fatalf("service source preview = %+v; want a cost past the zero budget", out)
	}
	unchanged(t)
	if out := preview(t, "lifecycle:\n  request_timeout_s: 20\n"); !out.Charged || out.AddedCents != 0 || out.NeedsApproval {
		t.Fatalf("a source with no standing cost = %+v; want free", out)
	}
	unchanged(t)

	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, srv.URL+"/v1/apps/"+app.Slug+"/deployments/source-tarball", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set(api.AgentPreviewHeader, api.AgentPreviewBudget)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("source-tarball preview = %d; want 400", resp.StatusCode)
	}
}
