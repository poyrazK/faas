package main

import (
	"context"
	"fmt"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

const (
	devPostgresReadyWait = 5 * time.Minute
	devPostgresReadyPoll = 2 * time.Second
)

// waitForDevPostgres holds the first sync of `gregale dev --postgres` until
// the developer database's bindings are ready. A binding writes its secret
// before it turns ready; without the wait the first release command ran
// before MIGRATION_DATABASE_URL existed, failed, and left a new environment
// with no release to serve. A control plane that is still provisioning after
// timeout lets the sync go ahead (the old behaviour); a failed binding stops.
func waitForDevPostgres(ctx context.Context, session api.DevSessionResponse, timeout, poll time.Duration, get func(context.Context) (api.DevSessionResponse, error), waiting func(name string)) (api.DevSessionResponse, error) {
	if session.Postgres == nil || session.Postgres.BindingState == "ready" {
		return session, nil
	}
	if waiting != nil {
		waiting(session.Postgres.Name)
	}
	deadline := time.Now().Add(timeout)
	for {
		switch state := session.Postgres.BindingState; state {
		case "ready":
			return session, nil
		case "failed", "deleting", "deleted":
			return session, fmt.Errorf("database %s binding is %s; run `gregale postgres bindings list %s` for its last error", session.Postgres.Name, state, session.Postgres.Name)
		}
		if !time.Now().Before(deadline) {
			return session, nil
		}
		select {
		case <-ctx.Done():
			return session, ctx.Err()
		case <-time.After(poll):
		}
		latest, err := get(ctx)
		if err != nil || latest.Postgres == nil {
			// A transient read error or an older control plane keeps the
			// last known state until the deadline.
			continue
		}
		session.Postgres = latest.Postgres
	}
}
