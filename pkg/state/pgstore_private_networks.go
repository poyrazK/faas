package state

import (
	"context"
	"errors"
	"net/netip"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

func scanPrivateNetwork(row interface{ Scan(...any) error }) (PrivateNetwork, error) {
	var (
		network                 PrivateNetwork
		accountID, cidr, status string
		statusDetail            string
	)
	err := row.Scan(&network.ID, &accountID, &network.Name, &network.Region, &cidr, &status, &statusDetail, &network.CreatedAt, &network.UpdatedAt)
	if err != nil {
		return PrivateNetwork{}, mapErr(err)
	}
	prefix, err := netip.ParsePrefix(cidr)
	if err != nil {
		return PrivateNetwork{}, err
	}
	network.AccountID, network.CIDR, network.Status, network.StatusDetail = accountID, prefix, status, statusDetail
	return network, nil
}

func scanPrivateNetworkAddress(row interface{ Scan(...any) error }) (PrivateNetworkAddress, error) {
	var (
		address                           PrivateNetworkAddress
		accountID, networkID, addressText string
	)
	err := row.Scan(&address.ID, &accountID, &networkID, &address.OwnerType, &address.OwnerID, &addressText, &address.CreatedAt)
	if err != nil {
		return PrivateNetworkAddress{}, mapErr(err)
	}
	parsed, err := parsePrivateNetworkAddress(addressText)
	if err != nil {
		return PrivateNetworkAddress{}, err
	}
	address.AccountID, address.NetworkID, address.Address = accountID, networkID, parsed
	return address, nil
}

func parsePrivateNetworkAddress(value string) (netip.Addr, error) {
	parsed, err := netip.ParseAddr(value)
	if err == nil {
		return parsed, nil
	}
	// PostgreSQL's inet text representation includes the host prefix
	// length (for example, "10.80.0.2/32"). Accept that canonical
	// representation as well as the bare address used by MemStore.
	prefix, prefixErr := netip.ParsePrefix(value)
	if prefixErr != nil {
		return netip.Addr{}, err
	}
	return prefix.Addr(), nil
}

func privateNetworkArgs(network PrivateNetwork) (string, pgtype.UUID, string, string, string, string, string) {
	return network.ID, mustPgUUID(network.AccountID), network.Name, network.Region, network.CIDR.String(), network.Status, network.StatusDetail
}

func (s *PgStore) CreatePrivateNetwork(ctx context.Context, network PrivateNetwork) (PrivateNetwork, error) {
	var err error
	network, err = validatePrivateNetwork(network)
	if err != nil {
		return PrivateNetwork{}, err
	}
	if network.ID == "" {
		network.ID = "net-" + uuid.NewString()
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return PrivateNetwork{}, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	accountID := mustPgUUID(network.AccountID)
	var lockedAccountID string
	if err := tx.QueryRow(ctx, `select id from accounts where id = $1 for update`, accountID).Scan(&lockedAccountID); err != nil {
		return PrivateNetwork{}, mapErr(err)
	}
	var exists bool
	if err := tx.QueryRow(ctx, `select exists(select 1 from private_networks where account_id = $1 and name = $2)`, accountID, network.Name).Scan(&exists); err != nil {
		return PrivateNetwork{}, err
	}
	if exists {
		return PrivateNetwork{}, ErrConflict
	}
	if err := tx.QueryRow(ctx, `select exists(select 1 from private_networks where account_id = $1 and region = $2 and cidr && $3::cidr)`, accountID, network.Region, network.CIDR.String()).Scan(&exists); err != nil {
		return PrivateNetwork{}, err
	}
	if exists {
		return PrivateNetwork{}, ErrConflict
	}
	id, _, name, region, cidr, status, detail := privateNetworkArgs(network)
	created, err := scanPrivateNetwork(tx.QueryRow(ctx, `
		insert into private_networks (id, account_id, name, region, cidr, status, status_detail)
		values ($1, $2, $3, $4, $5::cidr, $6, $7)
		returning id, account_id, name, region, cidr::text, status, status_detail, created_at, updated_at`, id, accountID, name, region, cidr, status, detail))
	if err != nil {
		return PrivateNetwork{}, mapErr(err)
	}
	if err := tx.Commit(ctx); err != nil {
		return PrivateNetwork{}, err
	}
	return created, nil
}

func (s *PgStore) GetPrivateNetwork(ctx context.Context, accountID, id string) (PrivateNetwork, error) {
	return scanPrivateNetwork(s.pool.QueryRow(ctx, `
		select id, account_id, name, region, cidr::text, status, status_detail, created_at, updated_at
		  from private_networks where account_id = $1 and id = $2`, mustPgUUID(accountID), strings.TrimSpace(id)))
}

func (s *PgStore) ListPrivateNetworks(ctx context.Context, accountID string) ([]PrivateNetwork, error) {
	rows, err := s.pool.Query(ctx, `
		select id, account_id, name, region, cidr::text, status, status_detail, created_at, updated_at
		  from private_networks where account_id = $1 order by name asc, id asc`, mustPgUUID(accountID))
	if err != nil {
		return nil, mapErr(err)
	}
	defer rows.Close()
	out := make([]PrivateNetwork, 0)
	for rows.Next() {
		network, scanErr := scanPrivateNetwork(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		out = append(out, network)
	}
	if err := rows.Err(); err != nil {
		return nil, mapErr(err)
	}
	return out, nil
}

func (s *PgStore) DeletePrivateNetwork(ctx context.Context, accountID, id string) error {
	result, err := s.pool.Exec(ctx, `delete from private_networks where account_id = $1 and id = $2 and not exists (select 1 from app_private_network_attachments a where a.account_id = $1 and a.network_id = $2)`, mustPgUUID(accountID), strings.TrimSpace(id))
	if err != nil {
		return mapErr(err)
	}
	if result.RowsAffected() > 0 {
		return nil
	}
	var exists bool
	if err := s.pool.QueryRow(ctx, `select exists(select 1 from private_networks where account_id = $1 and id = $2)`, mustPgUUID(accountID), strings.TrimSpace(id)).Scan(&exists); err != nil {
		return mapErr(err)
	}
	if !exists {
		return ErrNotFound
	}
	return ErrConflict
}

func (s *PgStore) AllocatePrivateNetworkAddress(ctx context.Context, accountID, networkID, ownerType, ownerID string) (PrivateNetworkAddress, error) {
	ownerType, ownerID = strings.TrimSpace(ownerType), strings.TrimSpace(ownerID)
	if !validPrivateNetworkOwner(ownerType, ownerID) {
		return PrivateNetworkAddress{}, ErrInvalidArgument
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return PrivateNetworkAddress{}, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	var prefixText string
	if err := tx.QueryRow(ctx, `select cidr::text from private_networks where account_id = $1 and id = $2 for update`, mustPgUUID(accountID), networkID).Scan(&prefixText); err != nil {
		return PrivateNetworkAddress{}, mapErr(err)
	}
	prefix, err := netip.ParsePrefix(prefixText)
	if err != nil {
		return PrivateNetworkAddress{}, err
	}
	var existingID, existingAccountID, existingNetworkID, existingOwnerType, existingOwnerID, existingAddress string
	var existingCreatedAt time.Time
	existingErr := tx.QueryRow(ctx, `select id, account_id, network_id, owner_type, owner_id, address::text, created_at from private_network_addresses where network_id = $1 and owner_type = $2 and owner_id = $3`, networkID, ownerType, ownerID).Scan(&existingID, &existingAccountID, &existingNetworkID, &existingOwnerType, &existingOwnerID, &existingAddress, &existingCreatedAt)
	if existingErr == nil {
		address, parseErr := parsePrivateNetworkAddress(existingAddress)
		if parseErr != nil {
			return PrivateNetworkAddress{}, parseErr
		}
		return PrivateNetworkAddress{ID: existingID, AccountID: existingAccountID, NetworkID: existingNetworkID, OwnerType: existingOwnerType, OwnerID: existingOwnerID, Address: address, CreatedAt: existingCreatedAt}, nil
	}
	if !errors.Is(existingErr, pgx.ErrNoRows) {
		return PrivateNetworkAddress{}, mapErr(existingErr)
	}
	usedRows, err := tx.Query(ctx, `select address::text from private_network_addresses where network_id = $1`, networkID)
	if err != nil {
		return PrivateNetworkAddress{}, err
	}
	used := map[netip.Addr]struct{}{}
	for usedRows.Next() {
		var value string
		if err := usedRows.Scan(&value); err != nil {
			usedRows.Close()
			return PrivateNetworkAddress{}, err
		}
		address, parseErr := parsePrivateNetworkAddress(value)
		if parseErr != nil {
			usedRows.Close()
			return PrivateNetworkAddress{}, parseErr
		}
		used[address] = struct{}{}
	}
	if err := usedRows.Err(); err != nil {
		usedRows.Close()
		return PrivateNetworkAddress{}, err
	}
	usedRows.Close()
	address, ok := allocatePrivateNetworkAddress(prefix, used)
	if !ok {
		return PrivateNetworkAddress{}, ErrConflict
	}
	id := uuid.NewString()
	row, err := scanPrivateNetworkAddress(tx.QueryRow(ctx, `
		insert into private_network_addresses (id, account_id, network_id, owner_type, owner_id, address)
		values ($1, $2, $3, $4, $5, $6::inet)
		returning id, account_id, network_id, owner_type, owner_id, address::text, created_at`, id, mustPgUUID(accountID), networkID, ownerType, ownerID, address.String()))
	if err != nil {
		return PrivateNetworkAddress{}, mapErr(err)
	}
	if err := tx.Commit(ctx); err != nil {
		return PrivateNetworkAddress{}, err
	}
	return row, nil
}

func (s *PgStore) ReleasePrivateNetworkAddress(ctx context.Context, accountID, networkID, ownerType, ownerID string) error {
	result, err := s.pool.Exec(ctx, `delete from private_network_addresses where account_id = $1 and network_id = $2 and owner_type = $3 and owner_id = $4`, mustPgUUID(accountID), networkID, strings.TrimSpace(ownerType), strings.TrimSpace(ownerID))
	if err != nil {
		return mapErr(err)
	}
	if result.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}
