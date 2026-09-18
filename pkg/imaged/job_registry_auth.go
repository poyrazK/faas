package imaged

import (
	"context"
	"errors"
	"fmt"

	"filippo.io/age"
	"github.com/onebox-faas/faas/pkg/logsanitize"
	"github.com/onebox-faas/faas/pkg/oci"
	"github.com/onebox-faas/faas/pkg/secretbox"
	"github.com/onebox-faas/faas/pkg/state"
)

// resolveJobRegistryAuth resolves the optional sealed credential for a job's
// source image registry. Job credentials use the same namespace and key
// rotation path as app credentials, but are scoped by (account, job, host).
func (h *Handler) resolveJobRegistryAuth(ctx context.Context, job state.Job, host string) (*oci.BasicAuth, error) {
	if h.secretboxIdentity == nil || host == "" {
		return nil, nil
	}
	store, ok := h.store.(state.JobRegistryCredentialStore)
	if !ok {
		return nil, nil
	}
	cred, err := store.GetJobRegistryCredential(ctx, job.AccountID, job.ID, host)
	if err != nil {
		if errors.Is(err, state.ErrNotFound) {
			return nil, nil
		}
		return nil, fmt.Errorf("imaged: lookup job registry credential for %s: %w", logsanitize.Field(host), err)
	}
	identities := h.secretboxIdentities
	if len(identities) == 0 && h.secretboxIdentity != nil {
		identities = []*age.X25519Identity{h.secretboxIdentity}
	}
	ns, plaintext, err := secretbox.OpenBytesMulti(identities, cred.PasswordEncrypted)
	if err != nil {
		return nil, fmt.Errorf("imaged: open job registry credential for %s: %w", logsanitize.Field(host), err)
	}
	if ns != "registry_creds" {
		return nil, fmt.Errorf("imaged: open job registry credential for %s: namespace=%q", logsanitize.Field(host), ns)
	}
	return &oci.BasicAuth{Username: cred.Username, Password: string(plaintext)}, nil
}

// markJobRegistryCredentialUsed records a successful authenticated materialization
// without making the build depend on metadata write availability.
func (h *Handler) markJobRegistryCredentialUsed(ctx context.Context, job state.Job, host string, auth *oci.BasicAuth) {
	if auth == nil || host == "" {
		return
	}
	store, ok := h.store.(state.JobRegistryCredentialStore)
	if !ok {
		return
	}
	if err := store.MarkJobRegistryCredentialUsed(ctx, job.AccountID, job.ID, host); err != nil && !errors.Is(err, state.ErrNotFound) {
		h.log.Warn("imaged: mark job registry credential used failed", "registry", logsanitize.Field(host), "job", job.ID, "err", err.Error())
	}
}
