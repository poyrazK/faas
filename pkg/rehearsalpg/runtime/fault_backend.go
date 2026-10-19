package runtime

import (
	"context"
	"time"

	"github.com/onebox-faas/faas/pkg/rehearsalpg"
	"github.com/onebox-faas/faas/pkg/rehearsalpg/runtime/sqlc"
)

// CutPostgres fences just this generated database before terminating its old
// connections. The administrative database and observer stay available.
func (p *Postgres) CutPostgres(ctx context.Context, r rehearsalpg.Resource, authorize func(context.Context) error) (int64, error) {
	if authorize == nil || !p.resourceNames(r) || r.Status != "ready" || !r.ExpiresAt.After(time.Now()) {
		return 0, ErrFixture
	}
	c, err := p.connect(ctx, "")
	if err != nil {
		return 0, err
	}
	defer func() { _ = c.Close(context.WithoutCancel(ctx)) }()
	q := sqlc.New()
	if q.LockFixtureDatabase(ctx, c, r.DatabaseName) != nil {
		return 0, ErrFixture
	}
	if err := authorize(ctx); err != nil {
		return 0, ErrFixture
	}
	command, err := q.FormatFreezeDatabase(ctx, c, r.DatabaseName)
	if execFormatted(ctx, c, command, err) != nil {
		return 0, ErrFixture
	}
	allowed, err := q.DatabaseConnectionsAllowed(ctx, c, r.DatabaseName)
	if err != nil || allowed {
		return 0, ErrFixture
	}
	closed, err := q.TerminateSessions(ctx, c, sqlc.TerminateSessionsParams{DatabaseName: r.DatabaseName, CollectorPid: 0})
	if err != nil {
		return 0, ErrFixture
	}
	for _, ok := range closed {
		if !ok {
			return 0, ErrFixture
		}
	}
	for {
		n, err := q.OtherSessions(ctx, c, sqlc.OtherSessionsParams{DatabaseName: r.DatabaseName, CollectorPid: 0})
		if err != nil {
			return 0, ErrFixture
		}
		if n == 0 {
			return int64(len(closed)), nil
		}
		timer := time.NewTimer(20 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return 0, ErrFixture
		case <-timer.C:
		}
	}
}

// RecoverPostgres never changes passwords, logins or another database. A fresh
// connection and explicit login/CONNECT check independently confirm recovery.
func (p *Postgres) RecoverPostgres(ctx context.Context, r rehearsalpg.Resource) error {
	if !p.resourceNames(r) {
		return ErrFixture
	}
	c, err := p.connect(ctx, "")
	if err != nil {
		return err
	}
	defer func() { _ = c.Close(context.WithoutCancel(ctx)) }()
	q := sqlc.New()
	if q.LockFixtureDatabase(ctx, c, r.DatabaseName) != nil {
		return ErrFixture
	}
	command, err := q.FormatRecoverDatabase(ctx, c, r.DatabaseName)
	if execFormatted(ctx, c, command, err) != nil {
		return ErrFixture
	}
	allowed, err := q.DatabaseConnectionsAllowed(ctx, c, r.DatabaseName)
	if err != nil || !allowed {
		return ErrFixture
	}
	probe, err := p.connect(ctx, r.DatabaseName)
	if err != nil {
		return ErrFixture
	}
	defer func() { _ = probe.Close(context.WithoutCancel(ctx)) }()
	ready, err := q.FaultAccessProbe(ctx, probe, sqlc.FaultAccessProbeParams{DatabaseName: r.DatabaseName, RoleName: r.RoleName})
	if err != nil || !ready.Valid || !ready.Bool {
		return ErrFixture
	}
	roles, err := q.ParticipantRoles(ctx, c, r.RoleName)
	if err != nil {
		return ErrFixture
	}
	for _, role := range roles {
		ready, err = q.FaultAccessProbe(ctx, probe, sqlc.FaultAccessProbeParams{DatabaseName: r.DatabaseName, RoleName: role})
		if err != nil || !ready.Valid || !ready.Bool {
			return ErrFixture
		}
	}
	return nil
}
