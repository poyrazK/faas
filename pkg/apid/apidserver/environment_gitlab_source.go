package apidserver

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/onebox-faas/faas/pkg/gitlabsource"
	"github.com/onebox-faas/faas/pkg/secretbox"
	"github.com/onebox-faas/faas/pkg/state"
)

const gitLabOAuthSecretNamespace = "gitlab-gitops-oauth"

func gitLabOAuthConfig() gitlabsource.OAuthConfig {
	return gitlabsource.OAuthConfig{
		ClientID:     os.Getenv("FAAS_GITLAB_OAUTH_CLIENT_ID"),
		ClientSecret: os.Getenv("FAAS_GITLAB_OAUTH_CLIENT_SECRET"),
		RedirectURI:  os.Getenv("FAAS_GITLAB_OAUTH_REDIRECT_URI"),
	}
}

func sealGitLabTokens(tokens gitlabsource.Tokens) ([]byte, error) {
	if setSecretRecipient == nil || hostSecretRecipient() == nil {
		return nil, fmt.Errorf("GitLab OAuth sealing unavailable")
	}
	data, err := json.Marshal(tokens)
	if err != nil {
		return nil, err
	}
	return secretbox.SealBytes(hostSecretRecipient(), gitLabOAuthSecretNamespace, data, 16384)
}

func openGitLabTokens(ctx context.Context, sealed []byte) (gitlabsource.Tokens, error) {
	var tokens gitlabsource.Tokens
	if hostIdentitiesForUnseal == nil {
		return tokens, fmt.Errorf("GitLab OAuth unsealing unavailable")
	}
	identities := hostIdentitiesForUnseal(ctx)
	if len(identities) == 0 {
		return tokens, fmt.Errorf("GitLab OAuth unsealing unavailable")
	}
	namespace, data, err := secretbox.OpenBytesMulti(identities, sealed)
	if err != nil || namespace != gitLabOAuthSecretNamespace {
		return tokens, fmt.Errorf("GitLab OAuth credentials unavailable")
	}
	if json.Unmarshal(data, &tokens) != nil || tokens.Access == "" || tokens.Refresh == "" || tokens.Expires.IsZero() {
		return tokens, fmt.Errorf("GitLab OAuth credentials invalid")
	}
	return tokens, nil
}

func (s *server) gitLabTokenForAccount(ctx context.Context, accountID string) (string, error) {
	store, ok := s.store.(state.GitLabOAuthStore)
	if !ok {
		return "", fmt.Errorf("GitLab OAuth store unavailable")
	}
	connection, err := store.GitLabOAuthConnection(ctx, accountID)
	if err != nil {
		return "", err
	}
	tokens, err := openGitLabTokens(ctx, connection.CredentialsSealed)
	if err != nil {
		return "", err
	}
	if time.Until(connection.ExpiresAt) > 2*time.Minute && time.Until(tokens.Expires) > 2*time.Minute {
		return tokens.Access, nil
	}
	config := gitLabOAuthConfig()
	if !config.Valid() {
		return "", fmt.Errorf("GitLab OAuth config unavailable")
	}
	replacement, refreshErr := config.Refresh(ctx, tokens.Refresh)
	if refreshErr != nil {
		// Another apid may have rotated this single-use refresh token. Re-read
		// once and accept only a newer stored generation.
		current, rereadErr := store.GitLabOAuthConnection(ctx, accountID)
		if rereadErr == nil && current.Version > connection.Version {
			newTokens, openErr := openGitLabTokens(ctx, current.CredentialsSealed)
			if openErr == nil && time.Until(current.ExpiresAt) > 2*time.Minute {
				return newTokens.Access, nil
			}
		}
		return "", fmt.Errorf("refresh GitLab OAuth: %w", gitlabsource.ErrUnavailable)
	}
	sealed, err := sealGitLabTokens(replacement)
	if err != nil {
		return "", err
	}
	_, err = store.RotateGitLabOAuthConnection(ctx, accountID, connection.Version, sealed, replacement.Expires)
	if err != nil {
		current, rereadErr := store.GitLabOAuthConnection(ctx, accountID)
		if rereadErr == nil && current.Version > connection.Version {
			newTokens, openErr := openGitLabTokens(ctx, current.CredentialsSealed)
			if openErr == nil && time.Until(current.ExpiresAt) > 2*time.Minute {
				return newTokens.Access, nil
			}
		}
		// A failed durable rotation leaves the old refresh token invalidated;
		// never use an unpersisted token for a long-lived Git source.
		return "", fmt.Errorf("persist GitLab OAuth rotation: %w", err)
	}
	return replacement.Access, nil
}

func (s *server) gitLabClient() gitlabsource.Client { return gitlabsource.Client{} }
