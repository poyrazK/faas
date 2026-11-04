package state

import (
	"context"
	"errors"
	"net/netip"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// PrivateNetworkMemberIdentity keeps the stable app service address separate
// from the unique source/return address of one VM. Only one VM owns Service.
type PrivateNetworkMemberIdentity struct {
	Address netip.Addr
	Service netip.Addr
}

type PrivateNetworkMemberStore interface {
	EnsurePrivateNetworkMember(context.Context, string, string, string, string, bool) (PrivateNetworkMemberIdentity, error)
	ReleasePrivateNetworkMember(context.Context, string, string, string, string, bool) error
}

type privateNetworkServiceOwner struct {
	AccountID, NetworkID, AppID, InstanceID, AddressID string
}

func privateNetworkServiceKey(networkID, appID string) string { return networkID + "\x00" + appID }

func (m *MemStore) EnsurePrivateNetworkMember(ctx context.Context, accountID, appID, networkID, instanceID string, eligible bool) (PrivateNetworkMemberIdentity, error) {
	if err := ctx.Err(); err != nil {
		return PrivateNetworkMemberIdentity{}, err
	}
	if !validPrivateNetworkOwner("instance", instanceID) {
		return PrivateNetworkMemberIdentity{}, ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	attachment, ok := m.privateNetworkAttachments[appID]
	if !ok || attachment.AccountID != accountID || attachment.NetworkID != networkID {
		return PrivateNetworkMemberIdentity{}, ErrNotFound
	}
	service, err := m.allocatePrivateNetworkAddressLocked(accountID, networkID, "app", appID)
	if err != nil {
		return PrivateNetworkMemberIdentity{}, err
	}
	member, err := m.allocatePrivateNetworkAddressLocked(accountID, networkID, "instance", instanceID)
	if err != nil {
		return PrivateNetworkMemberIdentity{}, err
	}
	result := PrivateNetworkMemberIdentity{Address: member.Address}
	if !eligible {
		return result, nil
	}
	key := privateNetworkServiceKey(networkID, appID)
	owner, claimed := m.privateNetworkServiceOwners[key]
	if !claimed {
		if m.privateNetworkServiceOwners == nil {
			m.privateNetworkServiceOwners = make(map[string]privateNetworkServiceOwner)
		}
		owner = privateNetworkServiceOwner{accountID, networkID, appID, instanceID, service.ID}
		m.privateNetworkServiceOwners[key] = owner
	}
	if owner.InstanceID == instanceID {
		result.Service = service.Address
	}
	return result, nil
}

// serviceOnly is used after removing only the VIP during a deployment switch;
// complete teardown additionally releases the instance's private source address.
func (m *MemStore) ReleasePrivateNetworkMember(ctx context.Context, accountID, appID, networkID, instanceID string, serviceOnly bool) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if networkID == "" && accountID == "" {
		m.mu.Lock()
		type identity struct{ account, app, network string }
		var identities []identity
		for _, a := range m.privateNetworkAddresses {
			if a.OwnerType == "instance" && a.OwnerID == instanceID {
				app := ""
				for _, o := range m.privateNetworkServiceOwners {
					if o.NetworkID == a.NetworkID && o.InstanceID == instanceID {
						app = o.AppID
					}
				}
				identities = append(identities, identity{a.AccountID, app, a.NetworkID})
			}
		}
		m.mu.Unlock()
		for _, id := range identities {
			if err := m.ReleasePrivateNetworkMember(ctx, id.account, id.app, id.network, instanceID, serviceOnly); err != nil {
				return err
			}
		}
		return nil
	}
	if networkID == "" {
		m.mu.Lock()
		networks := make(map[string]bool)
		for _, address := range m.privateNetworkAddresses {
			if address.AccountID == accountID && address.OwnerType == "instance" && address.OwnerID == instanceID {
				networks[address.NetworkID] = true
			}
		}
		for _, owner := range m.privateNetworkServiceOwners {
			if owner.AccountID == accountID && owner.InstanceID == instanceID {
				networks[owner.NetworkID] = true
			}
		}
		m.mu.Unlock()
		for network := range networks {
			if err := m.ReleasePrivateNetworkMember(ctx, accountID, appID, network, instanceID, serviceOnly); err != nil {
				return err
			}
		}
		return nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	key := privateNetworkServiceKey(networkID, appID)
	if owner, ok := m.privateNetworkServiceOwners[key]; ok && owner.AccountID == accountID && owner.InstanceID == instanceID {
		delete(m.privateNetworkServiceOwners, key)
	}
	_, claimed := m.privateNetworkServiceOwners[key]
	attachment, attached := m.privateNetworkAttachments[appID]
	for id, address := range m.privateNetworkAddresses {
		if address.AccountID != accountID || address.NetworkID != networkID {
			continue
		}
		if !serviceOnly && address.OwnerType == "instance" && address.OwnerID == instanceID {
			delete(m.privateNetworkAddresses, id)
		}
		if !claimed && (!attached || attachment.NetworkID != networkID) && address.OwnerType == "app" && address.OwnerID == appID {
			delete(m.privateNetworkAddresses, id)
		}
	}
	return nil
}

func (s *PgStore) EnsurePrivateNetworkMember(ctx context.Context, accountID, appID, networkID, instanceID string, eligible bool) (PrivateNetworkMemberIdentity, error) {
	if !validPrivateNetworkOwner("instance", instanceID) {
		return PrivateNetworkMemberIdentity{}, ErrInvalidArgument
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return PrivateNetworkMemberIdentity{}, err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	q := sqlc.New()
	if _, err := q.LockPrivateNetworkMemberAttachment(ctx, tx, sqlc.LockPrivateNetworkMemberAttachmentParams{AccountID: mustPgUUID(accountID), AppID: mustPgUUID(appID), NetworkID: networkID}); err != nil {
		return PrivateNetworkMemberIdentity{}, mapErr(err)
	}
	service, err := allocatePrivateNetworkAddressTx(ctx, tx, accountID, networkID, "app", appID)
	if err != nil {
		return PrivateNetworkMemberIdentity{}, err
	}
	member, err := allocatePrivateNetworkAddressTx(ctx, tx, accountID, networkID, "instance", instanceID)
	if err != nil {
		return PrivateNetworkMemberIdentity{}, err
	}
	result := PrivateNetworkMemberIdentity{Address: member.Address}
	if eligible {
		_, err = q.ClaimPrivateNetworkService(ctx, tx, sqlc.ClaimPrivateNetworkServiceParams{AccountID: mustPgUUID(accountID), NetworkID: networkID, AppID: mustPgUUID(appID), InstanceID: instanceID, ServiceAddressID: mustPgUUID(service.ID)})
		if err == nil {
			result.Service = service.Address
		} else if !errors.Is(err, pgx.ErrNoRows) {
			return PrivateNetworkMemberIdentity{}, mapErr(err)
		}
	}
	return result, tx.Commit(ctx)
}

func (s *PgStore) ReleasePrivateNetworkMember(ctx context.Context, accountID, appID, networkID, instanceID string, serviceOnly bool) error {
	if networkID == "" && accountID == "" {
		rows, err := sqlc.New().ListPrivateNetworkRuntimeMembers(ctx, s.pool, instanceID)
		if err != nil {
			return err
		}
		for _, row := range rows {
			if err := s.ReleasePrivateNetworkMember(ctx, pgUUIDString(row.AccountID), pgUUIDString(row.AppID), row.NetworkID, instanceID, serviceOnly); err != nil {
				return err
			}
		}
		return nil
	}

	if networkID == "" {
		networks, err := sqlc.New().ListPrivateNetworkInstanceNetworks(ctx, s.pool, sqlc.ListPrivateNetworkInstanceNetworksParams{AccountID: mustPgUUID(accountID), InstanceID: instanceID})
		if err != nil {
			return err
		}
		for _, network := range networks {
			if err := s.ReleasePrivateNetworkMember(ctx, accountID, appID, network, instanceID, serviceOnly); err != nil {
				return err
			}
		}
		return nil
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	q := sqlc.New()
	// Match allocation's lock order. Claim and release cannot wait on each
	// other's address/owner rows in the opposite order.
	if _, err := q.LockPrivateNetworkRuntimeDefinition(ctx, tx, sqlc.LockPrivateNetworkRuntimeDefinitionParams{AccountID: mustPgUUID(accountID), ID: networkID}); errors.Is(err, pgx.ErrNoRows) {
		return nil
	} else if err != nil {
		return mapErr(err)
	}
	if err := q.ReleasePrivateNetworkService(ctx, tx, sqlc.ReleasePrivateNetworkServiceParams{AccountID: mustPgUUID(accountID), NetworkID: networkID, AppID: mustPgUUID(appID), InstanceID: instanceID}); err != nil {
		return err
	}
	if !serviceOnly {
		if err := q.ReleasePrivateNetworkInstanceAddress(ctx, tx, sqlc.ReleasePrivateNetworkInstanceAddressParams{AccountID: mustPgUUID(accountID), NetworkID: networkID, OwnerID: instanceID}); err != nil {
			return err
		}
	}
	if err := q.ReleaseDetachedPrivateNetworkServiceAddress(ctx, tx, sqlc.ReleaseDetachedPrivateNetworkServiceAddressParams{AccountID: mustPgUUID(accountID), NetworkID: networkID, OwnerID: appID}); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
