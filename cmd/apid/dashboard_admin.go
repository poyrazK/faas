package main

import (
	"errors"
	"log/slog"
	"net/http"
	"os"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/dashboard"
	"github.com/onebox-faas/faas/pkg/httpsec"
	"github.com/onebox-faas/faas/pkg/state"
)

func tlsCutoverStateFile() string {
	if path := strings.TrimSpace(os.Getenv("FAAS_TLS_CUTOVER_STATE_FILE")); path != "" {
		return path
	}
	return dashboard.DefaultTLSCutoverStateFile
}

// renderAdminDashboard serves the operator-only dashboard page. The same
// email allowlist as /v1/admin/* protects it, so a valid customer session is
// not enough to inspect fleet cutover state.
func (s *server) renderAdminDashboard(w http.ResponseWriter, r *http.Request, log *slog.Logger, acct state.Account) {
	if allowed, problem := s.adminAllows(acct); !allowed {
		api.WriteProblem(w, problem)
		return
	}

	stateFile := tlsCutoverStateFile()
	cutover, err := dashboard.ReadTLSCutoverState(stateFile)
	present := err == nil
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		log.Warn("dashboard admin: read TLS cutover state", "path", stateFile, "err", err)
	}

	page := dashboard.Page{
		Title:   "Admin",
		Body:    "admin",
		Account: dashboardAccountView(acct, 0),
		Data: dashboard.AdminData{
			TLSCutover:          cutover,
			TLSCutoverStateFile: stateFile,
			TLSCutoverPresent:   present,
		},
	}
	if err := dashboard.Render(w, log, httpsec.NonceFromContext(r.Context()), page); err != nil {
		renderProblem(w, log, err)
	}
}
