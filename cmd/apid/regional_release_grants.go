package main

// adr: 481
import (
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/auth/middleware"
	"github.com/onebox-faas/faas/pkg/globaldirectory"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *server) requireRegionalReleasePrincipal(next accountHandler) accountHandler {
	return func(w http.ResponseWriter, r *http.Request, acct state.Account) {
		_, key, _ := middleware.AccountFromContext(r)
		if key != nil && key.RegionalReleaseGrantID != "" {
			if key.RegionalReleaseCellID != s.controlPlaneCell.ID {
				s.notFound(w, "no such regional app")
				return
			}
			next(w, r, acct)
			return
		}
		s.requireRegionalAccountKey(s.requireDirectoryAccount(next))(w, r, acct)
	}
}
func (s *server) putRegionalReleaseGrant(w http.ResponseWriter, r *http.Request, acct state.Account) {
	var req api.PutRegionalReleaseGrantRequest
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, api.RegionalReleaseGrantRequestMaxBytes))
	decoder.DisallowUnknownFields()
	hash, err := []byte(nil), error(nil)
	if decoder.Decode(&req) == nil && decoder.Decode(&struct{}{}) == io.EOF {
		hash, err = hex.DecodeString(req.TokenSHA256)
	}
	if err != nil || len(hash) != 32 || !globaldirectory.ValidID(req.GlobalAppID) || !globaldirectory.ValidID(r.PathValue("grant_id")) || req.ExpiresAt.IsZero() {
		api.WriteProblem(w, api.ErrValidation("send one bounded grant object with canonical grant/global UUIDs, a SHA-256 and expiry"))
		return
	}
	if duration := time.Until(req.ExpiresAt); duration > api.RegionalReleaseGrantMaxLifetime {
		api.WriteProblem(w, api.ErrValidation("regional grant lifetime exceeds the protocol bound").WithLimit(int64(api.RegionalReleaseGrantMaxLifetime/time.Second), int64((duration-1)/time.Second)+1).WithDocs("https://gregale.dev/docs/global-app-directory#regional-delegation"))
		return
	}
	store, ok := s.store.(state.RegionalReleaseGrantStore)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("regional release grants unavailable"))
		return
	}
	app, _ := r.Context().Value(cellDeploymentAppContextKey{}).(state.App)
	g, err := store.PutRegionalReleaseGrant(r.Context(), state.RegionalReleaseGrant{ID: r.PathValue("grant_id"), CellID: s.controlPlaneCell.ID, AppID: app.ID, AccountID: app.AccountID, IssuerAccountID: acct.ID, OrgID: app.OrgID, GlobalAppID: req.GlobalAppID, Hash: hash, ExpiresAt: req.ExpiresAt})
	s.writeRegionalReleaseGrant(w, r, acct, g, err)
}
func (s *server) getRegionalReleaseGrant(w http.ResponseWriter, r *http.Request, acct state.Account) {
	store, ok := s.store.(state.RegionalReleaseGrantStore)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("regional release grants unavailable"))
		return
	}
	if !globaldirectory.ValidID(r.PathValue("grant_id")) {
		api.WriteProblem(w, api.ErrValidation("use a canonical grant UUID"))
		return
	}
	g, err := store.GetRegionalReleaseGrant(r.Context(), acct.ID, s.controlPlaneCell.ID, r.PathValue("app_id"), r.PathValue("grant_id"))
	s.writeRegionalReleaseGrant(w, r, acct, g, err)
}
func (s *server) revokeRegionalReleaseGrant(w http.ResponseWriter, r *http.Request, acct state.Account) {
	store, ok := s.store.(state.RegionalReleaseGrantStore)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("regional release grants unavailable"))
		return
	}
	if !globaldirectory.ValidID(r.PathValue("grant_id")) {
		api.WriteProblem(w, api.ErrValidation("use a canonical grant UUID"))
		return
	}
	g, err := store.RevokeRegionalReleaseGrant(r.Context(), acct.ID, s.controlPlaneCell.ID, r.PathValue("app_id"), r.PathValue("grant_id"))
	s.writeRegionalReleaseGrant(w, r, acct, g, err)
}
func (s *server) writeRegionalReleaseGrant(w http.ResponseWriter, r *http.Request, acct state.Account, g state.RegionalReleaseGrant, err error) {
	var bound *state.RegionalReleaseGrantLimitError
	switch {
	case err == nil:
		status := g.Status
		if status == "active" && !g.ExpiresAt.After(time.Now()) {
			status = "expired"
		}
		if r.Method != http.MethodGet {
			s.audit.Emit(r.Context(), "regional_release_grant.changed", &acct.ID, map[string]any{"grant_id": g.ID, "app_id": g.AppID, "global_app_id": g.GlobalAppID, "status": status})
		}
		writeJSON(w, http.StatusOK, api.RegionalReleaseGrantResponse{GrantID: g.ID, CellID: g.CellID, RegionalAppID: g.AppID, GlobalAppID: g.GlobalAppID, ExpiresAt: g.ExpiresAt, Status: status})
	case errors.As(err, &bound):
		api.WriteProblem(w, api.NewProblem(http.StatusConflict, api.CodeRegionalReleaseGrantLimit, "Regional delegation limit reached", "Revoke an unused grant or wait for expiry.").WithLimit(bound.Limit, bound.Observed).WithDocs("https://gregale.dev/docs/global-app-directory#regional-delegation"))
	case errors.Is(err, state.ErrConflict):
		api.WriteProblem(w, api.NewProblem(http.StatusConflict, api.CodeConflict, "Grant identity conflicts", "This grant UUID, token hash or app stream is already registered with different identity."))
	case errors.Is(err, state.ErrNotFound):
		s.notFound(w, "no such regional grant or app")
	case errors.Is(err, state.ErrInvalidArgument):
		api.WriteProblem(w, api.ErrValidation("grant identity or expiry is invalid"))
	default:
		api.WriteProblem(w, api.ErrCapacity("could not change regional grant"))
	}
}
