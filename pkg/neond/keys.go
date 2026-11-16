package neond

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"sync"
	"time"

	"filippo.io/age"

	"github.com/onebox-faas/faas/pkg/secretbox"
)

// Project keys (ADR-20261112160000 decision 3): neond mints a Neon API key
// scoped to each project it creates, seals it to its own identity, stores it
// in neon_project_keys and calls that project's routes with it. A project
// without a key, or whose key Neon refuses, falls back to the admin key.

// sealNamespace tags a sealed project key, so a blob sealed for anything else
// never opens as one.
const sealNamespace = "neon-project-key"

// maxKeyBytes bounds a project key. Neon keys are well under 100 bytes.
const maxKeyBytes = 1024

// keyOpTimeout bounds minting or revoking one key. It runs after the
// forwarded call succeeded, detached from apid's deadline, so a key is not
// lost because apid stopped waiting.
const keyOpTimeout = 20 * time.Second

// StoredKey is a live row of neon_project_keys.
type StoredKey struct {
	KeyID  int64
	Sealed []byte
}

// KeyStore persists project keys. Only neond's database role can read or
// write the table.
type KeyStore interface {
	// LiveProjectKey returns the project's unrevoked key, if any.
	LiveProjectKey(ctx context.Context, project string) (StoredKey, bool, error)
	// InsertProjectKey records a newly minted key.
	InsertProjectKey(ctx context.Context, project string, keyID int64, sealed []byte) error
	// LiveProjectKeyIDs lists the project's unrevoked keys.
	LiveProjectKeyIDs(ctx context.Context, project string) ([]int64, error)
	// MarkProjectKeyRevoked records that Neon revoked the key.
	MarkProjectKeyRevoked(ctx context.Context, keyID int64) error
	// LiveProjectKeys lists every unrevoked key.
	LiveProjectKeys(ctx context.Context) ([]LiveKey, error)
	// ReplaceProjectKey retires oldKeyID and records newKeyID for the
	// project in one step. ErrKeyNotLive: oldKeyID is not its live key.
	ReplaceProjectKey(ctx context.Context, project string, oldKeyID, newKeyID int64, sealed []byte) error
}

// LiveKey is an unrevoked key, as the sweep sees it.
type LiveKey struct {
	KeyID     int64
	Project   string
	CreatedAt time.Time
}

// ErrKeyNotLive is a rotation of a key that is no longer the project's.
var ErrKeyNotLive = errors.New("neond: key is not the project's live key")

// ProjectKeys is what neond needs to mint, store and open project keys.
type ProjectKeys struct {
	// Org is the Neon organization the admin key's service user administers.
	Org string
	// Identities are the neon seal domain's (sealdomain.Neon): the current
	// identity first, which new keys are sealed to, then its previous twin
	// during a rotation. Only neond holds them.
	Identities []*age.X25519Identity
	Store      KeyStore
}

type projectKeys struct {
	ProjectKeys
	mu    sync.Mutex
	cache map[string]string
}

// UseProjectKeys turns on project keys. Call it before serving.
func (s *Server) UseProjectKeys(keys ProjectKeys) error {
	if keys.Org == "" || len(keys.Identities) == 0 || keys.Identities[0] == nil || keys.Store == nil || !validID.MatchString(keys.Org) {
		return errors.New("neond: invalid project key configuration")
	}
	s.keys = &projectKeys{ProjectKeys: keys, cache: map[string]string{}}
	return nil
}

// projectKey returns the project's key, opening and caching the stored one.
func (k *projectKeys) projectKey(ctx context.Context, project string) (string, bool, error) {
	k.mu.Lock()
	key, ok := k.cache[project]
	k.mu.Unlock()
	if ok {
		return key, true, nil
	}
	stored, ok, err := k.Store.LiveProjectKey(ctx, project)
	if err != nil || !ok {
		return "", false, err
	}
	namespace, plain, err := secretbox.OpenBytesMulti(k.Identities, stored.Sealed)
	if err != nil || namespace != sealNamespace || len(plain) == 0 {
		return "", false, fmt.Errorf("neond: open project key %d: sealed to another identity or not a project key", stored.KeyID)
	}
	key = string(plain)
	k.mu.Lock()
	k.cache[project] = key
	k.mu.Unlock()
	return key, true, nil
}

func (k *projectKeys) forget(project string) {
	k.mu.Lock()
	delete(k.cache, project)
	k.mu.Unlock()
}

// mintedKey is a key Neon just created, before neond stores it.
type mintedKey struct {
	ID  int64  `json:"id"`
	Key string `json:"key"`
}

// mintAtNeon creates a key scoped to project with the admin key.
func (s *Server) mintAtNeon(ctx context.Context, project string) (mintedKey, error) {
	body, err := json.Marshal(map[string]string{"key_name": "gregale-neond-" + project, "project_id": project})
	if err != nil {
		return mintedKey{}, err
	}
	code, payload, _, err := s.call(ctx, s.adminKey, http.MethodPost, "/organizations/"+s.keys.Org+"/api_keys", "", body)
	if err != nil {
		return mintedKey{}, err
	}
	if code != http.StatusOK && code != http.StatusCreated {
		return mintedKey{}, fmt.Errorf("neond: mint key for %s: Neon status %d", project, code)
	}
	var minted mintedKey
	if len(payload) > MaxResponseBytes || json.Unmarshal(payload, &minted) != nil || minted.ID <= 0 || minted.Key == "" {
		return mintedKey{}, fmt.Errorf("neond: mint key for %s: unreadable Neon reply", project)
	}
	return minted, nil
}

func (s *Server) seal(key string) ([]byte, error) {
	return secretbox.SealBytes(s.keys.Identities[0].Recipient(), sealNamespace, []byte(key), maxKeyBytes)
}

// mintProjectKey creates a key scoped to project with the admin key, seals
// it and stores it. A failure leaves the project on the admin key.
func (s *Server) mintProjectKey(ctx context.Context, project string) error {
	minted, err := s.mintAtNeon(ctx, project)
	if err != nil {
		return err
	}
	sealed, err := s.seal(minted.Key)
	if err == nil {
		err = s.keys.Store.InsertProjectKey(ctx, project, minted.ID, sealed)
	}
	if err != nil {
		// The key exists at Neon but not in the table: revoke it rather
		// than leave a live key nobody holds.
		if revokeErr := s.revokeKey(ctx, minted.ID); revokeErr != nil {
			return errors.Join(fmt.Errorf("neond: store key %d for %s: %w", minted.ID, project, err),
				fmt.Errorf("neond: revoke unstored key %d: %w", minted.ID, revokeErr))
		}
		return fmt.Errorf("neond: store key %d for %s: %w", minted.ID, project, err)
	}
	return nil
}

// revokeProjectKeys revokes every unrevoked key of a deleted project.
func (s *Server) revokeProjectKeys(ctx context.Context, project string) error {
	s.keys.forget(project)
	ids, err := s.keys.Store.LiveProjectKeyIDs(ctx, project)
	if err != nil {
		return err
	}
	var errs []error
	for _, id := range ids {
		if err := s.revokeKey(ctx, id); err != nil {
			errs = append(errs, err)
			continue
		}
		if err := s.keys.Store.MarkProjectKeyRevoked(ctx, id); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// revokeKey revokes one key at Neon. A key Neon no longer knows is revoked.
func (s *Server) revokeKey(ctx context.Context, id int64) error {
	code, _, _, err := s.call(ctx, s.adminKey, http.MethodDelete, "/organizations/"+s.keys.Org+"/api_keys/"+strconv.FormatInt(id, 10), "", nil)
	if err != nil {
		return err
	}
	if code != http.StatusOK && code != http.StatusNotFound {
		return fmt.Errorf("neond: revoke key %d: Neon status %d", id, code)
	}
	return nil
}

// createdProjectID reads the project ID from a create-project reply.
func createdProjectID(body []byte) (string, bool) {
	var reply struct {
		Project struct {
			ID string `json:"id"`
		} `json:"project"`
	}
	if json.Unmarshal(body, &reply) != nil || !validID.MatchString(reply.Project.ID) {
		return "", false
	}
	return reply.Project.ID, true
}
