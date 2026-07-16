package state

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"sync"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// MemStore is an in-memory Store for tests and local development. It is safe for
// concurrent use and enforces the same uniqueness constraints as the schema
// (unique email, unique slug, unique key hash) so tests exercise real error
// paths. It is NOT durable — production uses the Postgres store.
type MemStore struct {
	mu          sync.Mutex
	accounts    map[string]Account
	keys        map[string]APIKey // keyID -> key
	keyByHash   map[string]string // hex(hash) -> accountID
	apps        map[string]App    // appID -> app
	deployments map[string]Deployment
	idem        map[string]idemEntry // accountID+"\x00"+key -> entry
}

type idemEntry struct {
	status  int
	body    []byte
	created time.Time
}

// NewMemStore returns an empty in-memory store.
func NewMemStore() *MemStore {
	return &MemStore{
		accounts:    map[string]Account{},
		keys:        map[string]APIKey{},
		keyByHash:   map[string]string{},
		apps:        map[string]App{},
		deployments: map[string]Deployment{},
		idem:        map[string]idemEntry{},
	}
}

func newID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func (m *MemStore) CreateAccount(_ context.Context, email string, plan api.Plan) (Account, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, a := range m.accounts {
		if a.Email == email {
			return Account{}, fmt.Errorf("state: account with email %q exists", email)
		}
	}
	a := Account{ID: newID(), Email: email, Plan: plan, Status: AccountActive, CreatedAt: time.Now()}
	m.accounts[a.ID] = a
	return a, nil
}

func (m *MemStore) AccountByEmail(_ context.Context, email string) (Account, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, a := range m.accounts {
		if a.Email == email {
			return a, nil
		}
	}
	return Account{}, ErrNotFound
}

func (m *MemStore) AccountByKeyHash(_ context.Context, hash []byte) (Account, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	accountID, ok := m.keyByHash[hex.EncodeToString(hash)]
	if !ok {
		return Account{}, ErrNotFound
	}
	return m.accounts[accountID], nil
}

func (m *MemStore) CreateAPIKey(_ context.Context, accountID string, hash []byte, label string) (APIKey, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	h := hex.EncodeToString(hash)
	if _, dup := m.keyByHash[h]; dup {
		return APIKey{}, fmt.Errorf("state: duplicate key hash")
	}
	k := APIKey{ID: newID(), AccountID: accountID, Hash: hash, Label: label, CreatedAt: time.Now()}
	m.keys[k.ID] = k
	m.keyByHash[h] = accountID
	return k, nil
}

func (m *MemStore) DeleteAPIKey(_ context.Context, accountID, keyID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	k, ok := m.keys[keyID]
	if !ok || k.AccountID != accountID {
		return ErrNotFound
	}
	delete(m.keys, keyID)
	delete(m.keyByHash, hex.EncodeToString(k.Hash))
	return nil
}

func (m *MemStore) CreateApp(_ context.Context, app App) (App, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, a := range m.apps {
		if a.Slug == app.Slug && a.Status != AppDeleted {
			return App{}, fmt.Errorf("state: slug %q already taken", app.Slug)
		}
	}
	if app.ID == "" {
		app.ID = newID()
	}
	if app.CreatedAt.IsZero() {
		app.CreatedAt = time.Now()
	}
	m.apps[app.ID] = app
	return app, nil
}

func (m *MemStore) AppByID(_ context.Context, id string) (App, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	a, ok := m.apps[id]
	if !ok {
		return App{}, ErrNotFound
	}
	return a, nil
}

func (m *MemStore) AppBySlug(_ context.Context, slug string) (App, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, a := range m.apps {
		if a.Slug == slug && a.Status != AppDeleted {
			return a, nil
		}
	}
	return App{}, ErrNotFound
}

func (m *MemStore) ListApps(_ context.Context, accountID string) ([]App, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []App
	for _, a := range m.apps {
		if a.AccountID == accountID && a.Status != AppDeleted {
			out = append(out, a)
		}
	}
	return out, nil
}

func (m *MemStore) CountDeployedApps(_ context.Context, accountID string) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	n := 0
	for _, a := range m.apps {
		if a.AccountID == accountID && (a.Status == AppActive || a.Status == AppEvictedCold) {
			n++
		}
	}
	return n, nil
}

func (m *MemStore) DeleteApp(_ context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	a, ok := m.apps[id]
	if !ok {
		return ErrNotFound
	}
	a.Status = AppDeleted
	m.apps[id] = a
	return nil
}

func (m *MemStore) CreateDeployment(_ context.Context, d Deployment) (Deployment, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.apps[d.AppID]; !ok {
		return Deployment{}, fmt.Errorf("state: deployment for unknown app %q", d.AppID)
	}
	if d.ID == "" {
		d.ID = newID()
	}
	if d.CreatedAt.IsZero() {
		d.CreatedAt = time.Now()
	}
	m.deployments[d.ID] = d
	return d, nil
}

func (m *MemStore) LatestDeployment(_ context.Context, appID string) (Deployment, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var latest Deployment
	found := false
	for _, d := range m.deployments {
		if d.AppID == appID && (!found || d.CreatedAt.After(latest.CreatedAt)) {
			latest, found = d, true
		}
	}
	if !found {
		return Deployment{}, ErrNotFound
	}
	return latest, nil
}

func (m *MemStore) GetIdempotent(_ context.Context, accountID, key string) (int, []byte, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	e, ok := m.idem[accountID+"\x00"+key]
	if !ok || time.Since(e.created) > 24*time.Hour {
		return 0, nil, ErrNotFound
	}
	return e.status, e.body, nil
}

func (m *MemStore) PutIdempotent(_ context.Context, accountID, key string, status int, body []byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.idem[accountID+"\x00"+key] = idemEntry{status: status, body: append([]byte(nil), body...), created: time.Now()}
	return nil
}

// compile-time check that MemStore satisfies Store.
var _ Store = (*MemStore)(nil)
