package livequeries

import (
	"context"
	"errors"
	"time"

	"github.com/onebox-faas/faas/pkg/state/livekeys"
)

// MissingLogins is the store side of IssueMissingLogins; *livekeys.Store is
// one.
type MissingLogins interface {
	DatabasesWithoutLogin(ctx context.Context, limit int) ([]livekeys.LoginRef, error)
	IssueMissingLogin(ctx context.Context, accountID, databaseID string, issue func(context.Context) (*livekeys.Login, error)) (bool, error)
}

// missingLoginsPage bounds the databases one pass issues logins for; the
// next pass takes the rest.
const missingLoginsPage = 20

// MissingLoginsInterval is how often apid looks for live databases without a
// login.
const MissingLoginsInterval = 5 * time.Minute

// IssueMissingLogins issues a login, sealed bound to its database, to each
// database that has an active live key and no login, and returns how many it
// issued. The operator re-seal drops every legacy login instead of binding
// it (ADR-20261016151947, at-rest binding, amended 2026-10-28): a legacy
// blob names no database, so binding it would trust whatever row it sits on.
// A login apid issues comes from the provider for the database itself. A
// database whose login cannot be issued is logged and tried on the next
// pass.
func (s *Service) IssueMissingLogins(ctx context.Context, store MissingLogins) (int, error) {
	if s.Recipient == nil || s.Databases == nil {
		return 0, errors.New("live queries: no recipient or managed Postgres to issue logins with")
	}
	refs, err := store.DatabasesWithoutLogin(ctx, missingLoginsPage)
	if err != nil {
		return 0, err
	}
	issued := 0
	for _, ref := range refs {
		db, err := s.Databases.Get(ctx, ref.AccountID, ref.DatabaseID)
		if err == nil {
			var stored bool
			stored, err = store.IssueMissingLogin(ctx, ref.AccountID, ref.DatabaseID, func(ctx context.Context) (*livekeys.Login, error) {
				return s.issueLogin(ctx, db)
			})
			if stored {
				issued++
				s.audit(ctx, "managed_postgres.live.login_issued", ref.AccountID, map[string]any{"database_id": ref.DatabaseID})
			}
		}
		if err != nil && ctx.Err() == nil && s.Log != nil {
			s.Log.WarnContext(ctx, "live queries: could not issue a missing live login", "database", ref.DatabaseID, "err", err)
		}
	}
	return issued, ctx.Err()
}

// RunMissingLogins runs IssueMissingLogins every interval until ctx ends.
func (s *Service) RunMissingLogins(ctx context.Context, store MissingLogins, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		if _, err := s.IssueMissingLogins(ctx, store); err != nil && ctx.Err() == nil && s.Log != nil {
			s.Log.WarnContext(ctx, "live queries: missing live login pass failed", "err", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (s *Service) audit(ctx context.Context, action, accountID string, fields map[string]any) {
	if s.Audit != nil {
		s.Audit(ctx, action, accountID, fields)
	}
}
