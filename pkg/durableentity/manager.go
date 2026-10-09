package durableentity

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

type Options struct {
	LeaseDuration time.Duration
	// RetainedBytesLimit is an operator preview cap per entity, not a plan quota.
	// Zero leaves new entities uncapped. Existing persisted caps remain effective.
	RetainedBytesLimit int64
	// Now is an injectable clock for failure tests. Production uses time.Now.
	Now func() time.Time
}

type Manager struct {
	store        ObjectStore
	lease        time.Duration
	storageLimit int64
	now          func() time.Time
	mu           sync.Mutex
	locks        map[string]*entityLock
}

type entityLock struct {
	permit chan struct{}
	refs   int
}

// Open runs a conditional-write/read probe before returning a usable engine.
// A transient probe error fails startup; there is no unsafe bypass switch.
func Open(ctx context.Context, store ObjectStore, opts Options) (*Manager, error) {
	if store == nil {
		return nil, ErrInvalid
	}
	if opts.LeaseDuration == 0 {
		opts.LeaseDuration = api.DefaultDurableEntityLease
	}
	if opts.LeaseDuration <= 0 || opts.LeaseDuration > api.MaxDurableEntityLease || opts.RetainedBytesLimit < 0 {
		return nil, ErrInvalid
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	if err := probe(ctx, store); err != nil {
		return nil, fmt.Errorf("open durable entities: %w", err)
	}
	return &Manager{store: store, lease: opts.LeaseDuration, storageLimit: opts.RetainedBytesLimit, now: opts.Now, locks: make(map[string]*entityLock)}, nil
}

func probe(ctx context.Context, store ObjectStore) error {
	key := "gregale/durable-entities/v1/probes/" + uuid.NewString()
	first, second := []byte(`{"revision":1}`), []byte(`{"revision":2}`)
	version, err := store.Put(ctx, key, first, "")
	if err != nil {
		return err
	}
	if err := probeRead(ctx, store, key, first, version); err != nil {
		return err
	}
	if _, err := store.Put(ctx, key, second, ""); !errors.Is(err, ErrConflict) {
		return errors.Join(ErrUnsupported, err)
	}
	next, err := store.Put(ctx, key, second, version)
	if err != nil {
		return err
	}
	if next == version {
		return ErrUnsupported
	}
	if _, err := store.Put(ctx, key, first, version); !errors.Is(err, ErrConflict) {
		return errors.Join(ErrUnsupported, err)
	}
	return probeRead(ctx, store, key, second, next)
}

func probeRead(ctx context.Context, store ObjectStore, key string, expected []byte, version string) error {
	body, readVersion, err := store.Get(ctx, key, api.MaxDurableEntityManifestBytes)
	if err != nil {
		return err
	}
	if version == "" || readVersion != version || !bytes.Equal(body, expected) {
		return ErrUnsupported
	}
	return nil
}

func (m *Manager) lock(ctx context.Context, key string) (func(), error) {
	m.mu.Lock()
	l := m.locks[key]
	if l == nil {
		l = &entityLock{permit: make(chan struct{}, 1)}
		m.locks[key] = l
	}
	l.refs++
	m.mu.Unlock()
	select {
	case l.permit <- struct{}{}:
		return func() { <-l.permit; m.dropLock(key, l) }, nil
	case <-ctx.Done():
		m.dropLock(key, l)
		return nil, ctx.Err()
	}
}

func (m *Manager) dropLock(key string, l *entityLock) {
	m.mu.Lock()
	defer m.mu.Unlock()
	l.refs--
	if l.refs == 0 {
		delete(m.locks, key)
	}
}

func (m *Manager) readManifest(ctx context.Context, id ID) (manifest, string, error) {
	if !id.valid() {
		return manifest{}, "", ErrInvalid
	}
	body, etag, err := m.store.Get(ctx, id.prefix()+"manifest.json", api.MaxDurableEntityManifestBytes)
	if err != nil {
		return manifest{}, "", fmt.Errorf("read entity manifest: %w", err)
	}
	var value manifest
	if len(body) > api.MaxDurableEntityManifestBytes || etag == "" || json.Unmarshal(body, &value) != nil || !validManifest(id, value) {
		return manifest{}, "", ErrCorrupt
	}
	return value, etag, nil
}

func validManifest(id ID, value manifest) bool {
	if (value.Schema != 1 && value.Schema != 2 && value.Schema != 3 && value.Schema != 4) || value.Schema == 1 && value.Generation != 0 || value.ID != id || value.Epoch == 0 || !validUUID(value.Revision) || !validStorageMetadata(value) || !validAlarmDelivery(value) {
		return false
	}
	if value.OwnerID == "" {
		if value.Token != "" || !value.ExpiresAt.IsZero() {
			return false
		}
	} else if !validIdentity(value.OwnerID) || !validUUID(value.Token) || value.ExpiresAt.IsZero() {
		return false
	}
	if value.Version == 0 {
		return value.SnapshotKey == "" && value.SnapshotHash == ""
	}
	generation, ok := snapshotGeneration(id, value.SnapshotKey)
	return ok && generation <= value.Generation && validDigest(value.SnapshotHash)
}

const sha256HexLength = 64

func validUUID(value string) bool {
	parsed, err := uuid.Parse(value)
	return err == nil && parsed.String() == value && parsed != uuid.Nil
}

func (m *Manager) putManifest(ctx context.Context, value manifest, etag string) error {
	// Schema 4 fences older writers that cannot preserve alarm retry reservations.
	value.Schema = 4
	sealStorageUsage(&value)
	value.Revision = uuid.NewString()
	body, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("encode entity manifest: %w", err)
	}
	if len(body) > api.MaxDurableEntityManifestBytes {
		return ErrLimit
	}
	version, err := m.store.Put(ctx, value.ID.prefix()+"manifest.json", body, etag)
	if err != nil {
		return writeFailure("publish entity manifest", err)
	}
	if version == "" {
		return ErrUncertain
	}
	return nil
}
