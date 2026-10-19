package apidserver

import (
	"net/http"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/onebox-faas/faas/pkg/apid/dataapiusage"
	"github.com/onebox-faas/faas/pkg/state"
	usagestore "github.com/onebox-faas/faas/pkg/state/dataapiusage"
)

// configureDataAPIUsage wires ADR-20261019102108 Data API usage. Without a
// Postgres pool (MemStore development) both routes answer
// data_api_usage_unavailable.
func (s *server) configureDataAPIUsage(pool *pgxpool.Pool) {
	if pool != nil {
		s.dataAPIUsage = usagestore.New(pool)
	}
}

// reportDataAPIUsage is the private runtime surface; workload identity only.
func (s *server) reportDataAPIUsage(w http.ResponseWriter, r *http.Request) {
	handler := dataapiusage.ReportHandler{Verifier: s.workloadVerifier}
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
