package apidserver

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/livequeries"
	"github.com/onebox-faas/faas/pkg/managedpostgres"
	"github.com/onebox-faas/faas/pkg/platformflags"
	"github.com/onebox-faas/faas/pkg/realtime"
	"github.com/onebox-faas/faas/pkg/secretbox"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/livekeys"
)

// configureLiveQueries wires ADR-20261016151947 live-query enablement. Only accounts in
// FAAS_LIVE_QUERIES_ACCOUNTS see it at all (empty: nobody); for them, without
// managed Postgres or FAAS_APID_LIVE_KEY_RECIPIENT_FILE (livequeryd's age
// recipient) the routes answer live_queries_unavailable.
func (s *server) configureLiveQueries(pool *pgxpool.Pool, getenv func(string) string) error {
	rollout, err := livequeries.ParseRollout(getenv("FAAS_LIVE_QUERIES_ACCOUNTS"))
	if err != nil {
		return err
	}
	s.liveRollout = rollout
	all, accounts := rollout.Accounts()
	s.seedPlatformFlag(platformflags.LiveQueries, platformflags.FromAccountList(!rollout.Off(), all, accounts))
	svc := &livequeries.Service{
		DatabaseProblem: managedPostgresProblem,
		Audit: func(ctx context.Context, action, accountID string, fields map[string]any) {
			s.audit.Emit(ctx, action, &accountID, fields)
		},
		Log: s.log,
	}
	if s.managedPostgres != nil && pool != nil {
		store := livekeys.New(pool)
		svc.Databases = s.managedPostgres
		svc.Keys = store
		s.realtime.LiveRoutes = func(ctx context.Context, accountID, appID string) ([]realtime.LiveRoute, error) {
			return s.liveRoutes(ctx, store, accountID, appID)
		}
		svc.Changed = func(ctx context.Context, _, databaseID string) { s.resyncLiveEndpoints(ctx, store, databaseID) }
	}
	if path := getenv("FAAS_APID_LIVE_KEY_RECIPIENT_FILE"); path != "" {
		recipient, err := secretbox.LoadRecipient(path)
		if err != nil {
			return fmt.Errorf("live-query key recipient: %w", err)
		}
		svc.Recipient = recipient
	}
	// ADR 20261028161502: apid signs the live keys and logins it seals.
	if path := getenv("FAAS_APID_LIVE_SIGNING_KEY_FILE"); path != "" {
		key, err := livekeys.LoadSigningKey(path)
		if err != nil {
			return fmt.Errorf("live-query signing key: %w", err)
		}
		svc.Signer = livekeys.NewSigner(key)
	}
	s.liveQueries = svc
	return nil
}

// liveRoutes lists where an app's endpoints send live queries: the keys of
// live databases bound to the app, each with its database's hub. This is the
// check that an endpoint serves only databases its app is bound to.
func (s *server) liveRoutes(ctx context.Context, store *livekeys.Store, accountID, appID string) ([]realtime.LiveRoute, error) {
	if !s.liveGate().Allows(accountID) {
		return nil, nil // outside the rollout the protocol is not offered at all
	}
	keys, err := store.AppRoutes(ctx, accountID, appID)
	if err != nil {
		return nil, err
	}
	routes := make([]realtime.LiveRoute, 0, len(keys))
	access := s.dataAPIAccess(ctx, appID)
	for _, k := range keys {
		hub, err := s.managedPostgres.LiveHub(managedpostgres.Database{ID: k.DatabaseID, BackendID: k.BackendID, BackendFingerprint: k.BackendFingerprint})
		if err != nil || hub == "" {
			continue // the backend no longer runs live queries
		}
		if len(routes) == api.LiveQueryRoutesPerEndpoint {
			s.log.Warn("live routes truncated", "app_id", appID, "limit", api.LiveQueryRoutesPerEndpoint)
			break
		}
		route := realtime.LiveRoute{KID: k.KID, Database: k.DatabaseID, URL: hub, SealedRole: k.SealedRole, DataAPIRole: k.DataAPIRole}
		if k.DataAPIRole != "" {
			route.DataAPIAccess = access // the app's own Data API rules govern reads without a token
		}
		routes = append(routes, route)
	}
	return routes, nil
}

// resyncLiveEndpoints registers the endpoints of every app bound to a
// database again, after its keys changed. It runs detached from the request;
// the endpoint reconciler repairs anything it misses.
func (s *server) resyncLiveEndpoints(ctx context.Context, store *livekeys.Store, databaseID string) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
	go func() {
		defer cancel()
		apps, err := store.DatabaseApps(ctx, databaseID)
		if err != nil {
			s.log.Warn("live endpoint resync: list bound apps", "database_id", databaseID, "err", err)
			return
		}
		for _, app := range apps {
			if err := s.realtime.SyncAppEndpoints(ctx, app); err != nil {
				s.log.Warn("live endpoint resync", "database_id", databaseID, "app_id", app, "err", err)
			}
		}
	}()
}

// liveRoute and liveAccount read the gate per request: apid tests build the
// handler before they configure live queries, and an operator can change the
// live_queries platform flag at runtime.
func (s *server) liveRoute(next http.HandlerFunc) http.HandlerFunc {
	return livequeries.Route(s.liveGate, next)
}

func (s *server) liveAccount(next accountHandler) accountHandler {
	return livequeries.Account(s.liveGate, next)
}

// liveGate is the live_queries platform flag (ADR-1700) once an operator has
// written it, and FAAS_LIVE_QUERIES_ACCOUNTS until then.
func (s *server) liveGate() livequeries.Gate {
	snapshot := s.platformFlags().Snapshot()
	if def, version, ok := snapshot.Definition(platformflags.LiveQueries); ok && version > 0 {
		return liveFlagGate{snapshot: snapshot, everyone: def.Enabled && def.Default && len(def.Rules) == 0}
	}
	return s.liveRollout
}

// liveFlagGate adapts a platform-flag snapshot to livequeries.Gate. Only the
// account is known on the realtime path, so plan rules never match.
type liveFlagGate struct {
	snapshot *platformflags.Snapshot
	everyone bool
}

func (g liveFlagGate) Allows(accountID string) bool {
	return g.snapshot.Enabled(platformflags.LiveQueries, platformflags.Subject{AccountID: accountID})
}

func (g liveFlagGate) Off() bool { return !g.snapshot.GloballyEnabled(platformflags.LiveQueries) }

func (g liveFlagGate) Hidden(req *http.Request) bool {
	return !g.everyone && livequeries.IsLivePath(req.URL.Path)
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
