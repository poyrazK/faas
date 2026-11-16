package imaged

import (
	"context"
	"errors"
	"fmt"

	"github.com/onebox-faas/faas/pkg/logsanitize"
	"github.com/onebox-faas/faas/pkg/oci"
	"github.com/onebox-faas/faas/pkg/state"
)

// proxiedRegistryAuth is resolveRegistryAuth through secretd
// (ADR-20261110210000 slice 3j-2): when the app holds a credential for host,
// the pull goes through the control plane, which adds it; this node reads
// only that the credential exists, never its password.
func (h *Handler) proxiedRegistryAuth(ctx context.Context, app state.App, host string) (*oci.BasicAuth, error) {
	if host == "" {
		return nil, nil
	}
	if _, err := h.store.GetAppRegistryCredential(ctx, app.AccountID, app.ID, host); err != nil {
		if errors.Is(err, state.ErrNotFound) {
			return nil, nil
		}
		return nil, fmt.Errorf("imaged: lookup registry credential for %s: %w", logsanitize.Field(host), err)
	}
	return &oci.BasicAuth{Transport: h.registryProxy.For(app.ID)}, nil
}
