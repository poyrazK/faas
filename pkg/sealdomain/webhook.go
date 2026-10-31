package sealdomain

import (
	"errors"
	"fmt"

	"filippo.io/age"

	"github.com/onebox-faas/faas/pkg/secretbox"
)

// GitHub webhook HMAC secrets belong to the source domain. GitHub's
// verifier takes 16..64-byte keys.
const (
	GithubWebhookNamespace = "github_webhook_secret"
	GithubWebhookMaxBytes  = 64
)

// SealGithubWebhookSecret seals one webhook HMAC secret.
func SealGithubWebhookSecret(recipients []*age.X25519Recipient, secret []byte) ([]byte, error) {
	return secretbox.SealBytesTo(recipients, GithubWebhookNamespace, secret, GithubWebhookMaxBytes)
}

// OpenGithubWebhookSecret opens a sealed webhook secret and checks its
// namespace.
func OpenGithubWebhookSecret(identities []*age.X25519Identity, sealed []byte) ([]byte, error) {
	ns, plaintext, err := secretbox.OpenBytesMulti(identities, sealed)
	if err != nil {
		return nil, fmt.Errorf("sealdomain: open webhook secret: %w", err)
	}
	if ns != GithubWebhookNamespace || len(plaintext) == 0 {
		clear(plaintext)
		return nil, errors.New("sealdomain: webhook secret has the wrong namespace or is empty")
	}
	return plaintext, nil
}
