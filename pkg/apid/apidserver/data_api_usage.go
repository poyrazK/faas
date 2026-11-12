package apidserver

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/dataapibudget"
	"github.com/onebox-faas/faas/pkg/apid/dataapiusage"
	"github.com/onebox-faas/faas/pkg/state"
	budgetstore "github.com/onebox-faas/faas/pkg/state/dataapibudget"
	usagestore "github.com/onebox-faas/faas/pkg/state/dataapiusage"
)

// configureDataAPIUsage wires ADR-20261019102108 Data API usage. Without a
// Postgres pool (MemStore development) both routes answer
// data_api_usage_unavailable.
func (s *server) configureDataAPIUsage(pool *pgxpool.Pool) {
	if pool != nil {
		s.dataAPIUsage = usagestore.New(pool)
		s.dataAPIBudget = budgetstore.New(pool)
	}
}

func (s *server) admitDataAPIBudget(w http.ResponseWriter, r *http.Request) {
	h := dataapibudget.Handler{Verifier: s.workloadVerifier, Workloads: s.store}
	if s.dataAPIBudget != nil {
		h.Store = s.dataAPIBudget
	}
	h.ServeHTTP(w, r)
}

// dataAPIAccess is the access settings an app's Data API runtime last
// reported (ADR-20261016151947), or nil: without them realtime endpoints refuse
// anonymous and server-key live reads.
func (s *server) dataAPIAccess(ctx context.Context, appID string) *api.DataAPIAccessConfig {
	if s.dataAPIUsage == nil {
		return nil
	}
	raw, _, ok, err := s.dataAPIUsage.Access(ctx, appID)
	if err != nil {
		s.log.Warn("data api access settings unavailable", "app_id", appID, "err", err)
		return nil
	}
	var config api.DataAPIAccessConfig
	if !ok || json.Unmarshal(raw, &config) != nil || config.Validate() != nil {
		return nil
	}
	return &config
}

// forgetDataAPIAccess drops an app's reported access settings when its
// runtime can no longer report (usage reporting turned off), so live reads
// stop following settings it may since have changed.
func (s *server) forgetDataAPIAccess(ctx context.Context, accountID, appID string) {
	if s.dataAPIUsage == nil {
		return
	}
	if err := s.dataAPIUsage.DeleteAccess(ctx, accountID, appID); err != nil {
		s.log.Error("forget data api access settings", "app_id", appID, "err", err)
	}
	s.syncDataAPIAccess(ctx, appID)
}

func (s *server) syncDataAPIAccess(ctx context.Context, appID string) {
	if s.realtime.Host != nil {
		if err := s.realtime.SyncAppEndpoints(ctx, appID); err != nil {
			s.log.Warn("data api live access sync unavailable", "app_id", appID)
		}
	}
}

// reportDataAPIUsage is the private runtime surface; workload identity only.
func (s *server) reportDataAPIUsage(w http.ResponseWriter, r *http.Request) {
	handler := dataapiusage.ReportHandler{Verifier: s.workloadVerifier, Workloads: s.store, AccessChanged: s.syncDataAPIAccess}
	if s.dataAPIUsage != nil {
		handler.Store = s.dataAPIUsage
	}
	handler.ServeHTTP(w, r)
}

func (s *server) appDataAPIUsage(w http.ResponseWriter, r *http.Request, acct state.Account) {
	app, ok := s.loadApp(w, r, acct, r.PathValue("slug"))
	if !ok {
		return
	}
	handler := dataapiusage.QueryHandler{}
	if s.dataAPIUsage != nil {
		handler.Store = s.dataAPIUsage
	}
	handler.Serve(w, r, app.ID, app.Slug)
}
