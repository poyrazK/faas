package runtime

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/rehearsalpg"
	"github.com/onebox-faas/faas/pkg/rehearsalpg/runtime/sqlc"
	"github.com/onebox-faas/faas/pkg/rehearsalwork"
	"github.com/onebox-faas/faas/pkg/tasktoolbinding/taskacl"
)

func (p *Postgres) bindTaskParticipant(ctx context.Context, r rehearsalpg.Resource, app, password, access string) error {
	id, err := uuid.Parse(app)
	if err != nil || id.String() != app || app == r.AppID || !p.resourceNames(r) ||
		len(password) != 64 || !rehearsalwork.TaskPostgresAccess(access) {
		return ErrFixture
	}
	role := rehearsalwork.PostgresParticipantRole(r.ID, app, access)
	admin, err := p.connect(ctx, "")
	if err != nil {
		return ErrFixture
	}
	defer closeTaskParticipantConnection(ctx, admin)
	q := sqlc.New()
	// Use the same database and advisory lock as fixture fencing and deletion.
	if q.LockFixtureDatabase(ctx, admin, r.DatabaseName) != nil {
		return ErrFixture
	}
	conn, err := p.connect(ctx, r.DatabaseName)
	if err != nil {
		return ErrFixture
	}
	defer closeTaskParticipantConnection(ctx, conn)
	tx, err := conn.Begin(ctx)
	if err != nil {
		return ErrFixture
	}
	defer func() {
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), time.Second)
		defer cancel()
		_ = tx.Rollback(cleanup)
	}()

	// Only inspect catalogs and issue fixed DDL as the cluster administrator.
	// Fixture SQL and table data are never evaluated on this connection.
	if q.ConfigureTaskGrantTransaction(ctx, tx) != nil {
		return ErrFixture
	}
	read := taskacl.ReadTables(access == rehearsalwork.PostgresTaskObserver)
	valid, err := q.TaskGrantTablesReady(ctx, tx, sqlc.TaskGrantTablesReadyParams{OwnerRole: r.RoleName, TableNames: read})
	if err != nil || !valid {
		return ErrFixture
	}
	command, err := q.FormatConfigureTaskSchema(ctx, tx, r.RoleName)
	if execFormatted(ctx, tx, command, err) != nil {
		return ErrFixture
	}
	command, err = q.FormatCreateRole(ctx, tx, sqlc.FormatCreateRoleParams{RoleName: role, Password: password})
	if execFormatted(ctx, tx, command, err) != nil {
		return ErrFixture
	}
	command, err = q.FormatGrantTaskConnect(ctx, tx, sqlc.FormatGrantTaskConnectParams{DatabaseName: r.DatabaseName, RoleName: role})
	if execFormatted(ctx, tx, command, err) != nil {
		return ErrFixture
	}
	var write []string
	if access == rehearsalwork.PostgresTaskRuntime {
		for _, table := range read {
			if taskacl.Writable(table) {
				write = append(write, table)
			}
		}
	}
	command, err = q.FormatGrantTaskTables(ctx, tx, sqlc.FormatGrantTaskTablesParams{RoleName: role, ReadTables: read, WriteTables: write})
	if execFormatted(ctx, tx, command, err) != nil {
		return ErrFixture
	}
	if tx.Commit(ctx) != nil {
		return ErrFixture
	}
	return nil
}

func closeTaskParticipantConnection(ctx context.Context, conn *pgx.Conn) {
	cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), time.Second)
	defer cancel()
	_ = conn.Close(cleanup)
}
