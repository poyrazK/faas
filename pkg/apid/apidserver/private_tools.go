package apidserver

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"os"
	"slices"

	"filippo.io/age"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/toolregistry"
	"github.com/onebox-faas/faas/pkg/oci"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *server) newPrivateTools() *toolregistry.PrivateExecutor {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	transport.DialContext = oci.EgressDialContext(nil)
	return toolregistry.NewPrivateExecutor(s.store, toolregistry.PrivateExecutionOptions{Enabled: os.Getenv("FAAS_MCP_PRIVATE_TOOL_INVOCATION") == "1", Identities: hostIdentitiesForUnseal, ConnectionIdentities: func(context.Context) []*age.X25519Identity {
		return append([]*age.X25519Identity(nil), accountDomainIdentities...)
	}, ConnectionRecipients: toolConnectionRecipients, Endpoint: s.mcpReadinessPreview, VerifyPin: s.verifyPrivateToolPin, HTTP: &http.Client{Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}, Audit: s.agentConnectionDeps().Audit})
}

// New execution credentials have no fleet-key legacy. Always seal exclusively
// to a staged account key, independently of other account columns' rollout mode.
func toolConnectionRecipients() ([]*age.X25519Recipient, error) {
	account := accountDomainRecipient()
	if account == nil {
		return nil, errors.New("execution OAuth requires a staged account seal-domain identity")
	}
	for _, runtime := range []*age.X25519Recipient{hostSecretRecipient(), recipientOrNil(runtimeDomainRecipient)} {
		if runtime != nil && runtime.String() == account.String() {
			return nil, errors.New("execution OAuth account identity must differ from runtime and fleet identities")
		}
	}
	return []*age.X25519Recipient{account}, nil
}
func (s *server) stopPrivateTools() {
	if s.privateTools != nil {
		s.privateTools.Close()
	}
}

func (s *server) privateReplicaIDs(ctx context.Context, target api.ToolInvocationTarget) ([]string, error) {
	instances, err := s.store.ListInstancesForApp(ctx, target.AppID)
	if err != nil {
		return nil, err
	}
	ids := []string{}
	for _, instance := range instances {
		if instance.DeploymentID == target.DeploymentID && instance.State == string(state.StateRunning) {
			ids = append(ids, instance.ID)
		}
	}
	slices.Sort(ids)
	if len(ids) == 0 {
		return nil, state.ErrConflict
	}
	return ids, nil
}
func (s *server) verifyPrivateToolPin(ctx context.Context, target api.ToolInvocationTarget, endpoint string) error {
	u, err := url.Parse(endpoint)
	if err != nil || u.Scheme != "https" || !api.ValidMCPProbeHost(u.Host) {
		return state.ErrConflict
	}
	app, err := s.store.AppByID(ctx, target.AppID)
	if err != nil || app.Slug != target.App {
		return state.ErrConflict
	}
	ids, err := s.privateReplicaIDs(ctx, target)
	if err != nil {
		return err
	}
	for _, id := range ids {
		result, problem := s.probeMCPInstanceHealth(ctx, app, target.DeploymentID, id, u.Host)
		if problem != nil || result.HealthBodyTooLarge || result.HTTPStatus != http.StatusOK {
			return state.ErrConflict
		}
		var health struct {
			Status   string `json:"status"`
			SHA256   string `json:"catalog_sha256"`
			Revision *int64 `json:"catalog_revision"`
			Instance string `json:"instance_id"`
		}
		if json.Unmarshal([]byte(result.HealthBody), &health) != nil || health.Status != "ok" || health.SHA256 != target.CatalogSHA256 || health.Revision == nil || *health.Revision < 0 || health.Instance != "" && health.Instance != id {
			return state.ErrConflict
		}
	}
	current, err := s.privateReplicaIDs(ctx, target)
	if err != nil || !slices.Equal(ids, current) {
		return state.ErrConflict
	}
	return nil
}
