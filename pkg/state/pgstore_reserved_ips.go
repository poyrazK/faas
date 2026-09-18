package state

import (
	"context"
	"errors"
	"strings"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/networkip"
)

func scanReservedIP(row interface{ Scan(...any) error }) (ReservedIP, error) {
	var (
		lease                                 ReservedIP
		accountID, addressText, status, appID string
		nodeID, statusDetail                  string
	)
	if err := row.Scan(&lease.ID, &accountID, &lease.Region, &addressText, &status, &appID, &nodeID, &lease.Generation, &statusDetail, &lease.CreatedAt, &lease.UpdatedAt); err != nil {
		return ReservedIP{}, mapErr(err)
	}
	address, err := parsePrivateNetworkAddress(addressText)
	if err != nil {
		return ReservedIP{}, err
	}
	lease.AccountID, lease.Address, lease.Status = accountID, address, networkip.Status(status)
	lease.AppID, lease.NodeID, lease.StatusDetail = appID, nodeID, statusDetail
	return lease, nil
}

const reservedIPSelect = `
select id::text, account_id::text, region, address::text, status,
       coalesce(app_id::text, ''), coalesce(node_id, ''), generation,
       status_detail, created_at, updated_at
  from reserved_ip_leases`

func (s *PgStore) UpsertReservedIP(ctx context.Context, lease ReservedIP) (ReservedIP, error) {
	var err error
	lease, err = validateReservedIP(lease)
	if err != nil {
		return ReservedIP{}, err
	}
	if lease.ID == "" {
		lease.ID = uuid.NewString()
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return ReservedIP{}, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	current, currentErr := scanReservedIP(tx.QueryRow(ctx, reservedIPSelect+` where id = $1 for update`, lease.ID))
	if currentErr != nil && !errors.Is(currentErr, ErrNotFound) {
		return ReservedIP{}, currentErr
	}
	if currentErr == nil {
		if current.AccountID != lease.AccountID {
			return ReservedIP{}, ErrConflict
		}
		if current.AppID != "" && lease.AppID != "" && current.AppID != lease.AppID {
			return ReservedIP{}, ErrConflict
		}
		if current.AppID != "" {
			lease.AppID = current.AppID
			if lease.NodeID == "" {
				lease.NodeID = current.NodeID
			}
			if lease.Status == networkip.StatusAvailable {
				lease.Status = current.Status
			}
		}
		lease.CreatedAt = current.CreatedAt
		if lease.Generation < current.Generation {
			lease.Generation = current.Generation
		}
		appArg := nullableUUID(lease.AppID)
		row, updateErr := scanReservedIP(tx.QueryRow(ctx, `
update reserved_ip_leases
   set account_id = $2, region = $3, address = $4::inet, status = $5,
       app_id = $6, node_id = nullif($7, ''), generation = $8,
       status_detail = $9
 where id = $1
returning id::text, account_id::text, region, address::text, status,
          coalesce(app_id::text, ''), coalesce(node_id, ''), generation,
          status_detail, created_at, updated_at`, lease.ID, mustPgUUID(lease.AccountID), lease.Region, lease.Address.String(), string(lease.Status), appArg, lease.NodeID, lease.Generation, lease.StatusDetail))
		if updateErr != nil {
			return ReservedIP{}, mapErr(updateErr)
		}
		if err := tx.Commit(ctx); err != nil {
			return ReservedIP{}, err
		}
		return row, nil
	}
	row, err := scanReservedIP(tx.QueryRow(ctx, `
insert into reserved_ip_leases
       (id, account_id, region, address, status, app_id, node_id, generation, status_detail)
values ($1, $2, $3, $4::inet, $5, $6, nullif($7, ''), $8, $9)
returning id::text, account_id::text, region, address::text, status,
          coalesce(app_id::text, ''), coalesce(node_id, ''), generation,
          status_detail, created_at, updated_at`, lease.ID, mustPgUUID(lease.AccountID), lease.Region, lease.Address.String(), string(lease.Status), nullableUUID(lease.AppID), lease.NodeID, lease.Generation, lease.StatusDetail))
	if err != nil {
		return ReservedIP{}, mapErr(err)
	}
	if err := tx.Commit(ctx); err != nil {
		return ReservedIP{}, err
	}
	return row, nil
}

func (s *PgStore) GetReservedIP(ctx context.Context, accountID, id string) (ReservedIP, error) {
	return scanReservedIP(s.pool.QueryRow(ctx, reservedIPSelect+` where account_id = $1 and id = $2`, mustPgUUID(accountID), strings.TrimSpace(id)))
}

func (s *PgStore) ListReservedIPs(ctx context.Context, accountID, region string) ([]ReservedIP, error) {
	query := reservedIPSelect + ` where account_id = $1`
	args := []any{mustPgUUID(accountID)}
	if strings.TrimSpace(region) != "" {
		query += ` and region = $2`
		args = append(args, strings.TrimSpace(region))
	}
	query += ` order by region asc, address asc, id asc`
	rows, err := s.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, mapErr(err)
	}
	defer rows.Close()
	out := make([]ReservedIP, 0)
	for rows.Next() {
		lease, scanErr := scanReservedIP(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		out = append(out, lease)
	}
	if err := rows.Err(); err != nil {
		return nil, mapErr(err)
	}
	return out, nil
}

// ListReservedIPsForRouting returns the fleet-wide projection used by the
// route reconciler. It intentionally does not expose account-scoped list
// semantics: the reconciler must replace the complete desired route set for
// a region so released leases are withdrawn as well.
func (s *PgStore) ListReservedIPsForRouting(ctx context.Context, region string) ([]ReservedIP, error) {
	region = strings.TrimSpace(region)
	query := reservedIPSelect
	args := []any{}
	if region != "" {
		query += ` where region = $1`
		args = append(args, region)
	}
	query += ` order by region asc, address asc, id asc`
	rows, err := s.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, mapErr(err)
	}
	defer rows.Close()
	out := make([]ReservedIP, 0)
	for rows.Next() {
		lease, scanErr := scanReservedIP(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		out = append(out, lease)
	}
	if err := rows.Err(); err != nil {
		return nil, mapErr(err)
	}
	return out, nil
}

func (s *PgStore) AssignReservedIP(ctx context.Context, accountID, ipID, appID, nodeID string) (ReservedIP, error) {
	accountID, ipID, appID, nodeID = strings.TrimSpace(accountID), strings.TrimSpace(ipID), strings.TrimSpace(appID), strings.TrimSpace(nodeID)
	if accountID == "" || ipID == "" || appID == "" {
		return ReservedIP{}, ErrInvalidArgument
	}
	if _, err := parsePgUUID(appID); err != nil {
		return ReservedIP{}, ErrInvalidArgument
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return ReservedIP{}, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	lease, err := scanReservedIP(tx.QueryRow(ctx, reservedIPSelect+` where account_id = $1 and id = $2 for update`, mustPgUUID(accountID), ipID))
	if err != nil {
		return ReservedIP{}, err
	}
	if lease.AppID != "" && lease.AppID != appID {
		return ReservedIP{}, ErrConflict
	}
	var alreadyAssigned bool
	if err := tx.QueryRow(ctx, `select exists(select 1 from reserved_ip_leases where app_id = $1 and id <> $2)`, mustPgUUID(appID), ipID).Scan(&alreadyAssigned); err != nil {
		return ReservedIP{}, err
	}
	if alreadyAssigned {
		return ReservedIP{}, ErrConflict
	}
	if lease.Status == networkip.StatusAssigned && lease.AppID == appID {
		if nodeID == "" || nodeID == lease.NodeID {
			if err := tx.Commit(ctx); err != nil {
				return ReservedIP{}, err
			}
			return lease, nil
		}
	}
	if err := networkip.ValidateTransition(lease.Status, networkip.StatusPending); err != nil {
		return ReservedIP{}, ErrConflict
	}
	row, err := scanReservedIP(tx.QueryRow(ctx, `
update reserved_ip_leases
   set status = 'pending', app_id = $3, node_id = nullif($4, ''),
       generation = generation + 1, status_detail = 'assignment pending'
 where account_id = $1 and id = $2
returning id::text, account_id::text, region, address::text, status,
          coalesce(app_id::text, ''), coalesce(node_id, ''), generation,
          status_detail, created_at, updated_at`, mustPgUUID(accountID), ipID, mustPgUUID(appID), nodeID))
	if err != nil {
		return ReservedIP{}, mapErr(err)
	}
	if err := tx.Commit(ctx); err != nil {
		return ReservedIP{}, err
	}
	return row, nil
}

func (s *PgStore) ReleaseReservedIP(ctx context.Context, accountID, ipID, appID string) error {
	accountID, ipID, appID = strings.TrimSpace(accountID), strings.TrimSpace(ipID), strings.TrimSpace(appID)
	if accountID == "" || ipID == "" || appID == "" {
		return ErrInvalidArgument
	}
	if _, err := parsePgUUID(appID); err != nil {
		return ErrInvalidArgument
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	lease, err := scanReservedIP(tx.QueryRow(ctx, reservedIPSelect+` where account_id = $1 and id = $2 for update`, mustPgUUID(accountID), ipID))
	if err != nil {
		return err
	}
	if lease.AppID != appID {
		return ErrConflict
	}
	if err := networkip.ValidateTransition(lease.Status, networkip.StatusAvailable); err != nil {
		return ErrConflict
	}
	result, err := tx.Exec(ctx, `
update reserved_ip_leases
   set status = 'available', app_id = null, node_id = null,
       generation = generation + 1, status_detail = ''
 where account_id = $1 and id = $2`, mustPgUUID(accountID), ipID)
	if err != nil {
		return mapErr(err)
	}
	if result.RowsAffected() == 0 {
		return ErrNotFound
	}
	return tx.Commit(ctx)
}

func (s *PgStore) UpdateReservedIPStatus(ctx context.Context, accountID, ipID string, status networkip.Status, detail, nodeID string) (ReservedIP, error) {
	accountID, ipID, detail, nodeID = strings.TrimSpace(accountID), strings.TrimSpace(ipID), strings.TrimSpace(detail), strings.TrimSpace(nodeID)
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return ReservedIP{}, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	lease, err := scanReservedIP(tx.QueryRow(ctx, reservedIPSelect+` where account_id = $1 and id = $2 for update`, mustPgUUID(accountID), ipID))
	if err != nil {
		return ReservedIP{}, err
	}
	if err := networkip.ValidateTransition(lease.Status, status); err != nil {
		return ReservedIP{}, ErrConflict
	}
	if status == networkip.StatusAssigned && lease.AppID == "" {
		return ReservedIP{}, ErrInvalidArgument
	}
	appArg := nullableUUID(lease.AppID)
	if status == networkip.StatusAvailable {
		appArg = nil
		nodeID = ""
	} else if nodeID == "" {
		nodeID = lease.NodeID
	}
	row, err := scanReservedIP(tx.QueryRow(ctx, `
update reserved_ip_leases
   set status = $3, app_id = $4, node_id = nullif($5, ''),
       generation = generation + 1, status_detail = $6
 where account_id = $1 and id = $2
returning id::text, account_id::text, region, address::text, status,
          coalesce(app_id::text, ''), coalesce(node_id, ''), generation,
          status_detail, created_at, updated_at`, mustPgUUID(accountID), ipID, string(status), appArg, nodeID, detail))
	if err != nil {
		return ReservedIP{}, mapErr(err)
	}
	if err := tx.Commit(ctx); err != nil {
		return ReservedIP{}, err
	}
	return row, nil
}

// UpdateReservedIPStatusIfGeneration is the route reconciler's compare-and-
// swap transition. The row lock keeps the in-memory and Postgres stores on
// the same race-loser contract: a newer assignment or release returns
// ErrConflict and the next sweep re-reads the desired route.
func (s *PgStore) UpdateReservedIPStatusIfGeneration(ctx context.Context, accountID, ipID string, expectedGeneration int64, status networkip.Status, detail, nodeID string) (ReservedIP, error) {
	accountID, ipID, detail, nodeID = strings.TrimSpace(accountID), strings.TrimSpace(ipID), strings.TrimSpace(detail), strings.TrimSpace(nodeID)
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return ReservedIP{}, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	lease, err := scanReservedIP(tx.QueryRow(ctx, reservedIPSelect+` where account_id = $1 and id = $2 for update`, mustPgUUID(accountID), ipID))
	if err != nil {
		return ReservedIP{}, err
	}
	if lease.Generation != expectedGeneration {
		return ReservedIP{}, ErrConflict
	}
	if err := networkip.ValidateTransition(lease.Status, status); err != nil {
		return ReservedIP{}, ErrConflict
	}
	if status == networkip.StatusAssigned && lease.AppID == "" {
		return ReservedIP{}, ErrInvalidArgument
	}
	appArg := nullableUUID(lease.AppID)
	if status == networkip.StatusAvailable {
		appArg = nil
		nodeID = ""
	} else if nodeID == "" {
		nodeID = lease.NodeID
	}
	row, err := scanReservedIP(tx.QueryRow(ctx, `
update reserved_ip_leases
   set status = $3, app_id = $4, node_id = nullif($5, ''),
       generation = generation + 1, status_detail = $6
 where account_id = $1 and id = $2 and generation = $7
returning id::text, account_id::text, region, address::text, status,
          coalesce(app_id::text, ''), coalesce(node_id, ''), generation,
          status_detail, created_at, updated_at`, mustPgUUID(accountID), ipID, string(status), appArg, nodeID, detail, expectedGeneration))
	if err != nil {
		return ReservedIP{}, mapErr(err)
	}
	if err := tx.Commit(ctx); err != nil {
		return ReservedIP{}, err
	}
	return row, nil
}

func nullableUUID(value string) any {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	parsed, err := parsePgUUID(value)
	if err != nil {
		return nil
	}
	return parsed
}
