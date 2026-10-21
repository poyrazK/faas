// adr: 1139
package state

import (
	"context"
	"errors"
	"sort"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// ErrProjectEnvironmentSecretLimit is returned when a new key would exceed the
// plan's ProjectSecretCountMax for one project environment.
var ErrProjectEnvironmentSecretLimit = errors.New("state: project environment secret limit reached")

// ProjectEnvironmentSecret is one sealed secret shared by every service of a
// project environment (ADR-1139). Ciphertext is a single-key age envelope; it
// is set on writes and runtime reads and left empty in listings.
type ProjectEnvironmentSecret struct {
	AccountID, ProjectID, EnvironmentSlug, Key string
	Ciphertext                                 []byte
	Kid, ValueHash                             string
	SecretVersion                              int64
	CreatedAt, UpdatedAt                       time.Time
}

// ProjectEnvironmentSecretStore keeps project environment secrets. A set
// creates the key or replaces its value and advances its version; a new key
// fails with ErrProjectEnvironmentSecretLimit once the environment holds max
// keys. A missing project or environment is ErrNotFound.
type ProjectEnvironmentSecretStore interface {
	SetProjectEnvironmentSecret(ctx context.Context, secret ProjectEnvironmentSecret, max int) (ProjectEnvironmentSecret, error)
	DeleteProjectEnvironmentSecret(ctx context.Context, accountID, projectID, environmentSlug, key string) error
	ListProjectEnvironmentSecrets(ctx context.Context, accountID, projectID, environmentSlug string) ([]ProjectEnvironmentSecret, error)
}

func validProjectEnvironmentSecret(secret ProjectEnvironmentSecret, max int) bool {
	return secret.AccountID != "" && secret.ProjectID != "" && secret.EnvironmentSlug != "" &&
		api.ValidateEnvKey(secret.Key) == nil && len(secret.Ciphertext) > 0 &&
		secret.Kid != "" && secret.ValueHash != "" && max > 0
}

func projectEnvironmentSecretKey(projectID, environmentSlug, key string) string {
	return projectID + "\x00" + environmentSlug + "\x00" + key
}

func (m *MemStore) SetProjectEnvironmentSecret(_ context.Context, secret ProjectEnvironmentSecret, max int) (ProjectEnvironmentSecret, error) {
	if !validProjectEnvironmentSecret(secret, max) {
		return ProjectEnvironmentSecret{}, ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.projectEnvironmentOwnedLocked(secret.AccountID, secret.ProjectID, secret.EnvironmentSlug); err != nil {
		return ProjectEnvironmentSecret{}, err
	}
	if !m.secretEncryptionKeyAllowedLocked(secret.Kid) {
		return ProjectEnvironmentSecret{}, ErrConflict
	}
	if m.projectEnvironmentSecrets == nil {
		m.projectEnvironmentSecrets = map[string]ProjectEnvironmentSecret{}
	}
	id := projectEnvironmentSecretKey(secret.ProjectID, secret.EnvironmentSlug, secret.Key)
	now := time.Now().UTC()
	previous, exists := m.projectEnvironmentSecrets[id]
	if exists {
		secret.SecretVersion, secret.CreatedAt = previous.SecretVersion+1, previous.CreatedAt
	} else {
		if len(m.projectEnvironmentSecretsLocked(secret.ProjectID, secret.EnvironmentSlug)) >= max {
			return ProjectEnvironmentSecret{}, ErrProjectEnvironmentSecretLimit
		}
		secret.SecretVersion, secret.CreatedAt = 1, now
	}
	secret.UpdatedAt = now
	secret.Ciphertext = append([]byte(nil), secret.Ciphertext...)
	m.projectEnvironmentSecrets[id] = secret
	m.projectRuntimeChangeLocked(secret.ProjectID, secret.EnvironmentSlug, now)
	listed := secret
	listed.Ciphertext = nil
	return listed, nil
}

func (m *MemStore) DeleteProjectEnvironmentSecret(_ context.Context, accountID, projectID, environmentSlug, key string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	id := projectEnvironmentSecretKey(projectID, environmentSlug, key)
	secret, ok := m.projectEnvironmentSecrets[id]
	if !ok || secret.AccountID != accountID {
		return ErrNotFound
	}
	delete(m.projectEnvironmentSecrets, id)
	m.projectRuntimeChangeLocked(projectID, environmentSlug, time.Now().UTC())
	return nil
}

func (m *MemStore) ListProjectEnvironmentSecrets(_ context.Context, accountID, projectID, environmentSlug string) ([]ProjectEnvironmentSecret, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.projectEnvironmentOwnedLocked(accountID, projectID, environmentSlug); err != nil {
		return nil, err
	}
	secrets := m.projectEnvironmentSecretsLocked(projectID, environmentSlug)
	for i := range secrets {
		secrets[i].Ciphertext = nil
	}
	return secrets, nil
}

func (m *MemStore) projectEnvironmentOwnedLocked(accountID, projectID, environmentSlug string) error {
	project, ok := m.projects[projectID]
	if !ok || project.AccountID != accountID {
		return ErrNotFound
	}
	_, err := m.projectEnvironmentBySlugLocked(projectID, environmentSlug)
	return err
}

// projectEnvironmentSecretsLocked returns copies, ciphertext included, ordered
// by key.
func (m *MemStore) projectEnvironmentSecretsLocked(projectID, environmentSlug string) []ProjectEnvironmentSecret {
	var out []ProjectEnvironmentSecret
	for _, secret := range m.projectEnvironmentSecrets {
		if secret.ProjectID == projectID && secret.EnvironmentSlug == environmentSlug {
			secret.Ciphertext = append([]byte(nil), secret.Ciphertext...)
			out = append(out, secret)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	return out
}

// projectSecretRekeyRow is the reseal document of one project secret, the
// shape release_secret_project_document builds in Postgres.
type projectSecretRekeyRow struct {
	Key           string    `json:"key"`
	Kid           string    `json:"kid"`
	Ciphertext    []byte    `json:"ciphertext"`
	ValueHash     string    `json:"value_hash"`
	SecretVersion int64     `json:"secret_version"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
}

func projectSecretRekeyRowOf(p ProjectEnvironmentSecret) projectSecretRekeyRow {
	return projectSecretRekeyRow{p.Key, p.Kid, p.Ciphertext, p.ValueHash, p.SecretVersion, p.CreatedAt, p.UpdatedAt}
}

func (m *MemStore) deleteProjectEnvironmentSecretsLocked(projectID, environmentSlug string) {
	for id, secret := range m.projectEnvironmentSecrets {
		if secret.ProjectID == projectID && secret.EnvironmentSlug == environmentSlug {
			delete(m.projectEnvironmentSecrets, id)
		}
	}
}
