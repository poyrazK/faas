package runtime

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/rehearsalpg"
	"github.com/onebox-faas/faas/pkg/rehearsalpg/runtime/sqlc"
	"github.com/onebox-faas/faas/pkg/rehearsalwork"
	"github.com/onebox-faas/faas/pkg/tasktoolbinding/taskacl"
)

var errTaskParticipantPrivileges = errors.New("restricted Task database privileges could not be verified")

func writableTaskTables(read []string, observer bool) []string {
	write := []string{}
	if !observer {
		for _, table := range read {
			if taskacl.Writable(table) {
				write = append(write, table)
			}
		}
	}
	return write
}

func verifyTaskParticipants(ctx context.Context, tx pgx.Tx, q *sqlc.Queries, r rehearsalpg.Resource) error {
	for _, participant := range r.TaskParticipants {
		id, err := uuid.Parse(participant.AppID)
		if err != nil || id.String() != participant.AppID || id == uuid.Nil || participant.AppID == r.AppID ||
			!rehearsalwork.TaskPostgresAccess(participant.Access) {
			return errTaskParticipantPrivileges
		}
		observer := participant.Access == rehearsalwork.PostgresTaskObserver
		read := taskacl.ReadTables(observer)
		role := rehearsalwork.PostgresParticipantRole(r.ID, participant.AppID, participant.Access)
		valid, err := q.TaskParticipantPrivileges(ctx, tx, sqlc.TaskParticipantPrivilegesParams{
			RoleName: role, OwnerRole: r.RoleName, ReadTables: read, WriteTables: writableTaskTables(read, observer),
		})
		if err != nil || !valid {
			return errTaskParticipantPrivileges
		}
	}
	return nil
}

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
	observer := access == rehearsalwork.PostgresTaskObserver
	read := taskacl.ReadTables(observer)
	valid, err := q.TaskGrantObjectsReady(ctx, tx, sqlc.TaskGrantObjectsReadyParams{
		OwnerRole: r.RoleName, TableNames: read, RequireClaimOrder: !observer,
	})
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
	command, err = q.FormatGrantTaskObjects(ctx, tx, sqlc.FormatGrantTaskObjectsParams{
		RoleName: role, ReadTables: read, WriteTables: writableTaskTables(read, observer),
	})
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
