package state

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/networkip"
)

func scanReservedIPInventory(row interface{ Scan(...any) error }) (ReservedIPInventory, error) {
	var (
		inventory                    ReservedIPInventory
		addressText, status, leaseID string
	)
	if err := row.Scan(&inventory.ID, &inventory.Region, &addressText, &inventory.ProviderRef, &status, &leaseID, &inventory.StatusDetail, &inventory.CreatedAt, &inventory.UpdatedAt); err != nil {
		return ReservedIPInventory{}, mapErr(err)
	}
	address, err := parsePrivateNetworkAddress(addressText)
	if err != nil {
		return ReservedIPInventory{}, err
	}
	inventory.Address = address
	inventory.Status = networkip.InventoryStatus(status)
	inventory.LeaseID = leaseID
	return inventory, nil
}

const reservedIPInventorySelect = `
select id::text, region, address::text, provider_ref, status,
       coalesce(lease_id::text, ''), status_detail, created_at, updated_at
  from reserved_ip_inventory`

func (s *PgStore) UpsertReservedIPInventory(ctx context.Context, inventory ReservedIPInventory) (ReservedIPInventory, error) {
	var err error
	inventory, err = validateReservedIPInventory(inventory)
	if err != nil {
		return ReservedIPInventory{}, err
	}
	if inventory.ID == "" {
		inventory.ID = uuid.NewString()
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return ReservedIPInventory{}, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	current, currentErr := scanReservedIPInventory(tx.QueryRow(ctx, reservedIPInventorySelect+` where id = $1 for update`, inventory.ID))
	if currentErr != nil && !errors.Is(currentErr, ErrNotFound) {
		return ReservedIPInventory{}, currentErr
	}
	if currentErr == nil {
		if current.Status == networkip.InventoryClaimed {
			if current.Address != inventory.Address || current.Region != inventory.Region ||
				(inventory.LeaseID != "" && inventory.LeaseID != current.LeaseID) ||
				(inventory.Status != networkip.InventoryClaimed && inventory.Status != networkip.InventoryAvailable) {
				return ReservedIPInventory{}, ErrConflict
			}
			inventory.Status, inventory.LeaseID = current.Status, current.LeaseID
		}
		if inventory.ProviderRef == "" {
			inventory.ProviderRef = current.ProviderRef
		}
		inventory.CreatedAt = current.CreatedAt
		row, updateErr := scanReservedIPInventory(tx.QueryRow(ctx, `
update reserved_ip_inventory
   set region = $2, address = $3::inet, provider_ref = $4, status = $5,
       lease_id = $6, status_detail = $7
 where id = $1
returning id::text, region, address::text, provider_ref, status,
          coalesce(lease_id::text, ''), status_detail, created_at, updated_at`, inventory.ID, inventory.Region, inventory.Address.String(), inventory.ProviderRef, string(inventory.Status), nullableUUID(inventory.LeaseID), inventory.StatusDetail))
		if updateErr != nil {
			return ReservedIPInventory{}, mapErr(updateErr)
		}
		if err := tx.Commit(ctx); err != nil {
			return ReservedIPInventory{}, err
		}
		return row, nil
	}

	row, err := scanReservedIPInventory(tx.QueryRow(ctx, `
insert into reserved_ip_inventory
       (id, region, address, provider_ref, status, lease_id, status_detail)
values ($1, $2, $3::inet, $4, $5, $6, $7)
returning id::text, region, address::text, provider_ref, status,
          coalesce(lease_id::text, ''), status_detail, created_at, updated_at`, inventory.ID, inventory.Region, inventory.Address.String(), inventory.ProviderRef, string(inventory.Status), nullableUUID(inventory.LeaseID), inventory.StatusDetail))
	if err != nil {
		return ReservedIPInventory{}, mapErr(err)
	}
	if err := tx.Commit(ctx); err != nil {
		return ReservedIPInventory{}, err
	}
	return row, nil
}

func (s *PgStore) GetReservedIPInventory(ctx context.Context, id string) (ReservedIPInventory, error) {
	id = strings.TrimSpace(id)
	if _, err := parsePgUUID(id); err != nil {
		return ReservedIPInventory{}, ErrInvalidArgument
	}
	return scanReservedIPInventory(s.pool.QueryRow(ctx, reservedIPInventorySelect+` where id = $1`, mustPgUUID(id)))
}

func (s *PgStore) ListReservedIPInventory(ctx context.Context, region string, status networkip.InventoryStatus) ([]ReservedIPInventory, error) {
	region = strings.TrimSpace(region)
	if region != "" {
		if err := api.ValidatePrivateNetworkIdentifier(region); err != nil {
			return nil, ErrInvalidArgument
		}
	}
	if status != "" {
		if err := networkip.ValidateInventoryStatus(status); err != nil {
			return nil, ErrInvalidArgument
		}
	}
	query := reservedIPInventorySelect + ` where 1 = 1`
	args := make([]any, 0, 2)
	if region != "" {
		query += ` and region = $1`
		args = append(args, region)
	}
	if status != "" {
		query += ` and status = $` + strconv.Itoa(len(args)+1)
		args = append(args, string(status))
	}
	query += ` order by region asc, address asc, id asc`
	rows, err := s.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, mapErr(err)
	}
	defer rows.Close()
	out := make([]ReservedIPInventory, 0)
	for rows.Next() {
		item, scanErr := scanReservedIPInventory(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		out = append(out, item)
	}
	if err := rows.Err(); err != nil {
		return nil, mapErr(err)
	}
	return out, nil
}

func (s *PgStore) ClaimReservedIP(ctx context.Context, accountID, inventoryID string) (ReservedIP, error) {
	accountID, inventoryID = strings.TrimSpace(accountID), strings.TrimSpace(inventoryID)
	if _, err := parsePgUUID(accountID); err != nil {
		return ReservedIP{}, ErrInvalidArgument
	}
	if _, err := parsePgUUID(inventoryID); err != nil {
		return ReservedIP{}, ErrInvalidArgument
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return ReservedIP{}, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	inventory, err := scanReservedIPInventory(tx.QueryRow(ctx, reservedIPInventorySelect+` where id = $1 for update`, mustPgUUID(inventoryID)))
	if err != nil {
		return ReservedIP{}, err
	}
	if inventory.Status == networkip.InventoryClaimed {
		lease, leaseErr := scanReservedIP(tx.QueryRow(ctx, reservedIPSelect+` where id = $1 and account_id = $2`, mustPgUUID(inventory.LeaseID), mustPgUUID(accountID)))
		if leaseErr != nil {
			if errors.Is(leaseErr, ErrNotFound) {
				return ReservedIP{}, fmt.Errorf("%w: inventory claim has no lease", ErrConflict)
			}
			return ReservedIP{}, leaseErr
		}
		if err := tx.Commit(ctx); err != nil {
			return ReservedIP{}, err
		}
		return lease, nil
	}
	if inventory.Status == networkip.InventoryRetired {
		return ReservedIP{}, ErrConflict
	}
	leaseID := uuid.NewString()
	lease, err := scanReservedIP(tx.QueryRow(ctx, `
insert into reserved_ip_leases
       (id, account_id, region, address, status, status_detail)
values ($1, $2, $3, $4::inet, 'available', 'claimed from operator inventory')
returning id::text, account_id::text, region, address::text, status,
          coalesce(app_id::text, ''), coalesce(node_id, ''), generation,
          status_detail, created_at, updated_at`, leaseID, mustPgUUID(accountID), inventory.Region, inventory.Address.String()))
	if err != nil {
		return ReservedIP{}, mapErr(err)
	}
	if _, err := tx.Exec(ctx, `
update reserved_ip_inventory
   set status = 'claimed', lease_id = $2, status_detail = 'claimed'
 where id = $1`, mustPgUUID(inventoryID), mustPgUUID(lease.ID)); err != nil {
		return ReservedIP{}, mapErr(err)
	}
	if err := tx.Commit(ctx); err != nil {
		return ReservedIP{}, err
	}
	return lease, nil
}

func (s *PgStore) ReleaseReservedIPClaim(ctx context.Context, accountID, ipID string) error {
	accountID, ipID = strings.TrimSpace(accountID), strings.TrimSpace(ipID)
	if _, err := parsePgUUID(accountID); err != nil {
		return ErrInvalidArgument
	}
	if _, err := parsePgUUID(ipID); err != nil {
		return ErrInvalidArgument
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	lease, err := scanReservedIP(tx.QueryRow(ctx, reservedIPSelect+` where account_id = $1 and id = $2 for update`, mustPgUUID(accountID), mustPgUUID(ipID)))
	if err != nil {
		return err
	}
	if lease.AppID != "" {
		return ErrConflict
	}
	inventory, err := scanReservedIPInventory(tx.QueryRow(ctx, reservedIPInventorySelect+` where lease_id = $1 for update`, mustPgUUID(lease.ID)))
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return fmt.Errorf("%w: lease is not backed by inventory", ErrConflict)
		}
		return err
	}
	if _, err := tx.Exec(ctx, `
update reserved_ip_inventory
   set status = 'available', lease_id = null, status_detail = ''
 where id = $1`, mustPgUUID(inventory.ID)); err != nil {
		return mapErr(err)
	}
	if tag, err := tx.Exec(ctx, `delete from reserved_ip_leases where account_id = $1 and id = $2`, mustPgUUID(accountID), mustPgUUID(ipID)); err != nil {
		return mapErr(err)
	} else if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return tx.Commit(ctx)
}
