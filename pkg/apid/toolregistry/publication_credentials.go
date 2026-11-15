package toolregistry

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/secretbox"
	"github.com/onebox-faas/faas/pkg/state"
)

func (e *PrivateExecutor) publicationCredential(ctx context.Context, account string, v api.ToolPublicationVerification) (connectionAccess, []string, error) {
	authority, err := e.waitPublicationCredential(ctx, account, v)
	if err != nil {
		return connectionAccess{}, nil, err
	}
	if authority.Secret != nil {
		values, err := secretbox.OpenMulti(e.opts.Identities(ctx), authority.Secret.Ciphertext)
		token := values[authority.Secret.Key]
		if err != nil || !executionToken(token) || !api.ValidMCPProbeToken(token) {
			return connectionAccess{}, nil, state.ErrNotFound
		}
		return connectionAccess{token: token}, []string{token}, nil
	}
	if authority.Connection == nil {
		return connectionAccess{}, nil, state.ErrNotFound
	}
	prior, err := e.connectionSensitiveValues(ctx, *authority.Connection)
	if err != nil {
		if authority.RenewalFresh {
			if store, ok := e.store.(state.ToolOAuthConnectionStore); ok {
				persist, cancel := context.WithTimeout(context.WithoutCancel(ctx), api.ToolOAuthPersistTimeout)
				defer cancel()
				_, _ = store.FinishToolOAuthRenewal(persist, account, authority.Connection.Record.ID, authority.Connection.LeaseID, nil, nil, "credential_unavailable")
			}
		}
		return connectionAccess{}, nil, err
	}
	guard := func(next http.RoundTripper) http.RoundTripper {
		if next == nil {
			next = http.DefaultTransport
		}
		return publicationRenewalTransport{executor: e, account: account, verification: v, lease: authority.Connection.LeaseID, next: next}
	}
	access, err := e.useConnectionClaim(ctx, account, state.ToolOAuthConnectionClaim{Connection: *authority.Connection, Fresh: authority.RenewalFresh}, guard)
	if err != nil {
		return connectionAccess{}, nil, err
	}
	current, err := e.publicationChange(ctx, account, v.PublicationID, v.KeyID, "dispatch", state.ToolPublicationVerificationChange{})
	if err != nil || current.Connection == nil {
		return connectionAccess{}, nil, state.ErrNotFound
	}
	values, err := e.connectionSensitiveValues(ctx, *current.Connection)
	if err != nil {
		return connectionAccess{}, nil, err
	}
	return access, append(append(prior, values...), access.token), nil
}

func (e *PrivateExecutor) waitPublicationCredential(ctx context.Context, account string, v api.ToolPublicationVerification) (state.ToolPublicationVerificationResult, error) {
	for {
		out, err := e.publicationChange(ctx, account, v.PublicationID, v.KeyID, "renew", state.ToolPublicationVerificationChange{})
		if !errors.Is(err, state.ErrToolOAuthBusy) {
			return out, err
		}
		timer := time.NewTimer(api.ToolInvocationCancelPoll)
		select {
		case <-ctx.Done():
			timer.Stop()
			return out, ctx.Err()
		case <-timer.C:
		}
	}
}

type publicationRenewalTransport struct {
	executor     *PrivateExecutor
	account      string
	verification api.ToolPublicationVerification
	lease        string
	next         http.RoundTripper
}

func (t publicationRenewalTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	_, err := t.executor.publicationChange(r.Context(), t.account, t.verification.PublicationID, t.verification.KeyID, "renew_dispatch", state.ToolPublicationVerificationChange{LeaseID: t.lease})
	if err != nil {
		return nil, err
	}
	return t.next.RoundTrip(r)
}

func publicationCatalogSensitive(catalog []byte, values []string) bool {
	for _, value := range values {
		if value == "" {
			continue
		}
		escaped, _ := json.Marshal(value)
		if bytes.Contains(catalog, []byte(value)) || len(escaped) > 2 && bytes.Contains(catalog, escaped[1:len(escaped)-1]) {
			return true
		}
	}
	return false
}
