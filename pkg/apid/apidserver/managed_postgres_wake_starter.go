package apidserver

import (
	"context"
	"log/slog"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/onebox-faas/faas/pkg/db"
)

// runManagedPostgresWakeStarter hands schedd's instance_changed notifications
// to the managed PostgreSQL wake starter (ADR-20261125105038). A lost
// notification only means the database starts on the app's first query, so
// a plain reconnecting LISTEN is enough. Without managed PostgreSQL, or a
// store that keeps the setting, it does nothing.
func runManagedPostgresWakeStarter(ctx context.Context, pool *pgxpool.Pool, srv *server, log *slog.Logger) error {
	if srv.managedPostgres == nil || !srv.managedPostgres.HasWakeStarter() {
		return nil
	}
	ch, err := db.SubscribeWithReconnect(ctx, pool, []string{db.NotifyInstanceChanged}, log)
	if err != nil {
		return err
	}
	payloads := make(chan string, 256)
	go func() {
		defer close(payloads)
		for {
			select {
			case <-ctx.Done():
				return
			case n, ok := <-ch:
				if !ok {
					return
				}
				select {
				case payloads <- n.Payload:
				default:
					// A burst of transitions: drop rather than delay the
					// LISTEN; a later transition of the app retries.
				}
			}
		}
	}()
	srv.managedPostgres.RunWakeStarter(ctx, payloads)
	return nil
}
