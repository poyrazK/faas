// Package connectioncapacity reads connection ceilings through an already
// authenticated maintenance connection. It never changes roles or settings.
package connectioncapacity

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/managedpostgres/connectioncapacity/sqlc"
)

var ErrObservation = errors.New("connection capacity SQL observation unavailable")

// Observation is a ceiling, not free slots or a count of active consumers.
// PostgreSQL settings alone cannot qualify a provider's tenant allocation.
type Observation struct {
	MaxConnections    int64 `json:"max_connections"`
	SuperuserReserved int64 `json:"superuser_reserved"`
	Reserved          int64 `json:"reserved"`
	DatabaseLimit     int64 `json:"database_limit"`
	RoleLimit         int64 `json:"role_limit"`
}

func (o Observation) Valid() bool {
	return o.MaxConnections > 0 && o.SuperuserReserved >= 0 && o.Reserved >= 0 &&
		o.SuperuserReserved <= o.MaxConnections && o.Reserved <= o.MaxConnections-o.SuperuserReserved &&
		o.DatabaseLimit >= -1 && o.RoleLimit >= -1
}

func (o Observation) Ceiling(allocation int64) int64 {
	limit := min(allocation, o.MaxConnections-o.SuperuserReserved-o.Reserved)
	for _, bound := range []int64{o.DatabaseLimit, o.RoleLimit} {
		if bound >= 0 {
			limit = min(limit, bound)
		}
	}
	return limit
}

// Read uses a read-only transaction and verifies both database and authenticated
// role. The caller owns the bounded context and the connection's lifetime.
func Read(ctx context.Context, conn *pgx.Conn, database, role string, major int) (Observation, error) {
	if conn == nil || database == "" || role == "" || major < 16 {
		return Observation{}, ErrObservation
	}
	tx, err := conn.BeginTx(ctx, pgx.TxOptions{AccessMode: pgx.ReadOnly})
	if err != nil {
		return Observation{}, ErrObservation
	}
	defer func() { _ = tx.Rollback(ctx) }()
	row, err := sqlc.New().ReadConnectionCapacity(ctx, tx)
	if err != nil || row.DatabaseName != database || row.RoleName != role || row.SessionRole != role || int(row.ServerVersion)/10000 != major {
		return Observation{}, ErrObservation
	}
	out := Observation{MaxConnections: row.MaxConnections, SuperuserReserved: row.SuperuserReserved,
		Reserved: row.Reserved, DatabaseLimit: row.DatabaseLimit, RoleLimit: row.RoleLimit}
	if !out.Valid() || tx.Commit(ctx) != nil {
		return Observation{}, ErrObservation
	}
	return out, nil
}
