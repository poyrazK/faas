package liveserver

import (
	"context"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"time"

	"filippo.io/age"

	"github.com/onebox-faas/faas/pkg/livequery/seal"
	"github.com/onebox-faas/faas/pkg/secretbox"
	"github.com/onebox-faas/faas/pkg/state/livekeys"
)

// KeySource returns a database's sealed live keys; (*livekeys.Store).Ring
// is one.
type KeySource func(ctx context.Context, databaseID string) ([]livekeys.SealedKey, error)

// WatchKeys keeps the hub's key ring equal to the keys apid stored for its
// database, reading them every interval until ctx ends. It opens each key
// with identities (livequeryd's age identity). A key that does not open is
// skipped and logged. A failed read keeps the last ring, so a control-plane
// outage does not stop live queries; nothing can be revoked during one
// either, since apid is down with it.
func WatchKeys(ctx context.Context, hub *Hub, source KeySource, identities []*age.X25519Identity, every time.Duration, log *slog.Logger) {
	if every <= 0 {
		every = 5 * time.Second
	}
	last := "-" // matches no ring, so the first read always applies
	load := func() {
		keys, err := source(ctx, hub.cfg.Database)
		if err != nil {
			if ctx.Err() == nil {
				log.Warn("livequeryd: reading live keys failed; keeping the current ones", "err", err)
				hub.cfg.Metrics.keyRefresh("error")
			}
			return
		}
		ring, id := OpenKeys(hub.cfg.Database, keys, identities, log)
		hub.cfg.Metrics.keyRefresh("ok")
		if id == last {
			return
		}
		hub.SetKeyring(ctx, ring)
		hub.cfg.Metrics.setKeys(ring.Len())
		last = id
		log.Info("livequeryd: live keys loaded", "keys", ring.Len())
	}
	load()
	tick := time.NewTicker(every)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			load()
		}
	}
}

// OpenKeys opens a database's sealed keys into a key ring. It returns an
// identity of the ring's contents (key IDs and retirement times) so callers
// can tell when it changed.
func OpenKeys(database string, keys []livekeys.SealedKey, identities []*age.X25519Identity, log *slog.Logger) (*seal.Keyring, string) {
	var (
		opened []seal.Key
		ids    []string
	)
	for _, k := range keys {
		secret, err := openKey(k, database, identities)
		if err != nil {
			log.Error("livequeryd: a live key does not open; seals made with it are refused", "kid", k.KID, "err", err)
			continue
		}
		opened = append(opened, seal.Key{Database: database, Secret: secret, Retires: k.RetiresAt})
		ids = append(ids, fmt.Sprintf("%s@%d", k.KID, k.RetiresAt.Unix()))
	}
	ring, err := seal.NewKeyring(opened...)
	if err != nil {
		// Key IDs are a primary key, so duplicates cannot come from the store.
		log.Error("livequeryd: live keys form no key ring", "err", err)
		ring, _ = seal.NewKeyring()
		ids = nil
	}
	slices.Sort(ids)
	return ring, strings.Join(ids, ",")
}

// openKey opens one of database's live keys. A key sealed to another
// database's namespace is refused, so a row copied between databases does
// not join this one's ring.
func openKey(k livekeys.SealedKey, database string, identities []*age.X25519Identity) (seal.Secret, error) {
	namespace, plain, err := secretbox.OpenBytesMulti(identities, k.Sealed)
	if err != nil {
		return seal.Secret{}, err
	}
	defer clear(plain)
	if !livekeys.KeyNamespaceOpens(namespace, database) {
		return seal.Secret{}, fmt.Errorf("sealed for %q, not this database's live queries", namespace)
	}
	secret, err := seal.ParseSecret(string(plain))
	if err != nil {
		return seal.Secret{}, err
	}
	if secret.KID != k.KID {
		return seal.Secret{}, fmt.Errorf("the secret's key ID is %s", secret.KID)
	}
	return secret, nil
}
