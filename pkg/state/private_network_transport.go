package state

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

type PrivateNetworkTransportStore interface {
	EnsurePrivateNetworkTransportVNI(context.Context, string, string, string) (uint32, error)
}

// PreferredPrivateNetworkVNI preserves the existing identity when it is free.
// The durable allocator resolves collisions and retains every reservation.
func PreferredPrivateNetworkVNI(accountID, networkID string) uint32 {
	sum := sha256.Sum256([]byte("transport-vni\x00" + strings.TrimSpace(accountID) + "\x00" + strings.TrimSpace(networkID)))
	return binary.BigEndian.Uint32(sum[:4])%api.PrivateNetworkMaxTransportVNI + 1
}

type privateNetworkTransportIdentity struct {
	AccountID, Region string
	VNI               uint32
}

func (m *MemStore) EnsurePrivateNetworkTransportVNI(ctx context.Context, accountID, networkID, region string) (uint32, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	network, ok := m.privateNetworks[networkID]
	if !ok || network.AccountID != accountID || network.Region != region {
		return 0, ErrNotFound
	}
	if prior, ok := m.privateNetworkTransportIDs[networkID]; ok {
		if prior.AccountID != accountID || prior.Region != region {
			return 0, ErrConflict
		}
		return prior.VNI, nil
	}
	used := make(map[uint32]bool)
	for _, identity := range m.privateNetworkTransportIDs {
		if identity.Region == region {
			used[identity.VNI] = true
		}
	}
	vni := PreferredPrivateNetworkVNI(accountID, networkID)
	for attempts := 0; attempts < api.PrivateNetworkMaxTransportVNI; attempts++ {
		if !used[vni] {
			if m.privateNetworkTransportIDs == nil {
				m.privateNetworkTransportIDs = make(map[string]privateNetworkTransportIdentity)
			}
			m.privateNetworkTransportIDs[networkID] = privateNetworkTransportIdentity{accountID, region, vni}
			return vni, nil
		}
		vni = vni%api.PrivateNetworkMaxTransportVNI + 1
	}
	return 0, ErrConflict
}

func (s *PgStore) EnsurePrivateNetworkTransportVNI(ctx context.Context, accountID, networkID, region string) (uint32, error) {
	q := sqlc.New()
	params := sqlc.GetPrivateNetworkTransportIdentityParams{AccountID: mustPgUUID(accountID), NetworkID: networkID, Region: region}
	if vni, err := q.GetPrivateNetworkTransportIdentity(ctx, s.pool, params); err == nil {
		return uint32(vni), nil
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return 0, mapErr(err)
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	if err := q.LockPrivateNetworkTransportRegion(ctx, tx, region); err != nil {
		return 0, err
	}
	if _, err := q.LockPrivateNetworkTransportDefinition(ctx, tx, sqlc.LockPrivateNetworkTransportDefinitionParams{AccountID: mustPgUUID(accountID), ID: networkID, Region: region}); err != nil {
		return 0, mapErr(err)
	}
	if vni, err := q.GetPrivateNetworkTransportIdentity(ctx, tx, params); err == nil {
		return uint32(vni), tx.Commit(ctx)
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return 0, mapErr(err)
	}
	preferred := PreferredPrivateNetworkVNI(accountID, networkID)
	for attempts := 0; attempts < api.PrivateNetworkMaxTransportVNI; attempts++ {
		if err := ctx.Err(); err != nil {
			return 0, err
		}
		vni, err := q.InsertPrivateNetworkTransportIdentity(ctx, tx, sqlc.InsertPrivateNetworkTransportIdentityParams{NetworkID: networkID, AccountID: mustPgUUID(accountID), Region: region, Vni: int32(preferred)})
		if err == nil {
			return uint32(vni), tx.Commit(ctx)
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return 0, mapErr(err)
		}
		// A conflicting network ID is never reassigned, including tombstones.
		if exists, lookupErr := q.PrivateNetworkTransportIdentityExists(ctx, tx, networkID); lookupErr != nil {
			return 0, lookupErr
		} else if exists {
			return 0, ErrConflict
		}
		preferred = preferred%api.PrivateNetworkMaxTransportVNI + 1
	}
	return 0, ErrConflict
}
