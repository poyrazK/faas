package apidserver

import (
	"context"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/projectsecrets"
	"github.com/onebox-faas/faas/pkg/state"
)

// projectSecretsHost adapts *server to projectsecrets.Host (ADR-1139). It
// reads server fields at call time, so tests that swap s.store behave as
// they do for the other surfaces.
type projectSecretsHost struct{ s *server }

var _ projectsecrets.Host = projectSecretsHost{}

func (h projectSecretsHost) Store() state.Store { return h.s.store }

func (h projectSecretsHost) EmitAudit(ctx context.Context, kind string, accountID *string, data map[string]any) {
	h.s.audit.Emit(ctx, kind, accountID, data)
}

func (h projectSecretsHost) Seal(accountID, key, value string, maxBytes int) (projectsecrets.Sealed, *api.Problem) {
	ciphertext, kid, valueHash, problem := h.s.sealSecretValue("project secret", accountID, key, value, maxBytes)
	return projectsecrets.Sealed{Ciphertext: ciphertext, Kid: kid, ValueHash: valueHash}, problem
}

func (s *server) projectSecrets() projectsecrets.Service {
	return projectsecrets.Service{Host: projectSecretsHost{s}}
}
