package apidserver

import (
	"context"
	"fmt"
	"net/http"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/onebox-faas/faas/pkg/apid/livequeries"
	"github.com/onebox-faas/faas/pkg/secretbox"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/livekeys"
)

// configureLiveQueries wires ADR-1384 live-query enablement. Without managed
// Postgres or FAAS_APID_LIVE_KEY_RECIPIENT_FILE (livequeryd's age recipient)
// the routes answer live_queries_unavailable.
func (s *server) configureLiveQueries(pool *pgxpool.Pool, getenv func(string) string) error {
	svc := &livequeries.Service{
		DatabaseProblem: managedPostgresProblem,
		Audit: func(ctx context.Context, action, accountID string, fields map[string]any) {
			s.audit.Emit(ctx, action, &accountID, fields)
		},
		Log: s.log,
	}
	if s.managedPostgres != nil && pool != nil {
		svc.Databases = s.managedPostgres
		svc.Keys = livekeys.New(pool)
	}
	if path := getenv("FAAS_APID_LIVE_KEY_RECIPIENT_FILE"); path != "" {
		recipient, err := secretbox.LoadRecipient(path)
		if err != nil {
			return fmt.Errorf("live-query key recipient: %w", err)
		}
		svc.Recipient = recipient
	}
	s.liveQueries = svc
	return nil
}

func (s *server) getLiveQueries(w http.ResponseWriter, r *http.Request, acct state.Account) {
	s.liveQueries.Get(w, r, acct)
}

func (s *server) enableLiveQueries(w http.ResponseWriter, r *http.Request, acct state.Account) {
	s.liveQueries.Enable(w, r, acct)
}

func (s *server) rotateLiveSecret(w http.ResponseWriter, r *http.Request, acct state.Account) {
	s.liveQueries.Rotate(w, r, acct)
}

func (s *server) disableLiveQueries(w http.ResponseWriter, r *http.Request, acct state.Account) {
	s.liveQueries.Disable(w, r, acct)
}
