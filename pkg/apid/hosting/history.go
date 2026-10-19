package hosting

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strconv"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/httpkit"
	"github.com/onebox-faas/faas/pkg/state"
)

type HistoryHost interface {
	HostingHistoryEnabled() bool
	HostingHistoryStore() state.HostingHistoryStore
}

func (s Service) historyEnabled() bool {
	h, ok := s.Host.(HistoryHost)
	return ok && h.HostingHistoryEnabled()
}
func (s Service) historyHost(w http.ResponseWriter, a state.Account) (state.HostingHistoryStore, bool) {
	if !s.guard(w, a) {
		return nil, false
	}
	h, ok := s.Host.(HistoryHost)
	if !ok || !h.HostingHistoryEnabled() {
		api.WriteProblem(w, api.NewProblem(503, "frontend_hosting_history_disabled", "Release history unavailable", "internal release provenance and history are disabled"))
		return nil, false
	}
	if h.HostingHistoryStore() == nil {
		api.WriteProblem(w, api.NewProblem(503, "frontend_hosting_history_unconfigured", "Release history unavailable", "upgrade all hosting writers before enabling release history"))
		return nil, false
	}
	return h.HostingHistoryStore(), true
}

// RequireReleaseProvenance applies the metadata opt-in before idempotency replay.
// The original bounded request is restored for the normal decoder and key hash.
func (s Service) RequireReleaseProvenance(next httpkit.AccountHandler) httpkit.AccountHandler {
	return func(w http.ResponseWriter, r *http.Request, a state.Account) {
		if !s.guard(w, a) {
			return
		}
		if r.Body != nil {
			body, err := io.ReadAll(io.LimitReader(r.Body, api.HostingReleaseRequestMaxBytes+1))
			_ = r.Body.Close()
			if err != nil || int64(len(body)) > api.HostingReleaseRequestMaxBytes {
				api.WriteProblem(w, api.ErrValidation("release request exceeds its limit"))
				return
			}
			r.Body = io.NopCloser(bytes.NewReader(body))
			var req struct {
				Provenance json.RawMessage `json:"provenance"`
			}
			if json.Unmarshal(body, &req) == nil && len(req.Provenance) > 0 && string(req.Provenance) != "null" {
				if _, ok := s.historyHost(w, a); !ok {
					return
				}
			}
		}
		next(w, r, a)
	}
}
func (s Service) ListHistory(w http.ResponseWriter, r *http.Request, a state.Account) {
	store, ok := s.historyHost(w, a)
	if !ok {
		return
	}
	site, ok := s.site(w, r, a)
	if !ok {
		return
	}
	before := int64(0)
	if raw, exists := r.URL.Query()["before_revision"]; exists {
		var err error
		if len(raw) != 1 {
			api.WriteProblem(w, api.ErrValidation("one before_revision is required"))
			return
		}
		before, err = strconv.ParseInt(raw[0], 10, 64)
		if err != nil || before < 1 || before > (1<<53)-1 {
			api.WriteProblem(w, api.ErrValidation("before_revision must be a positive safe integer"))
			return
		}
	}
	page, err := store.ListHostingHistory(r.Context(), a.ID, site.ID, before)
	if err != nil {
		failure(w, err)
		return
	}
	httpkit.WriteJSON(w, http.StatusOK, page)
}
func hostingActor(r *http.Request, a state.Account) api.HostingPromotionActor {
	actor := api.HostingPromotionActor{AccountID: a.ID}
	if principal, ok := httpkit.PrincipalFrom(r); ok {
		actor.AccountID = principal.Acct.ID
		if principal.Key != nil {
			actor.KeyID = principal.Key.ID
		}
	}
	return actor
}

func (s Service) createReleaseIntent(w http.ResponseWriter, r *http.Request, a state.Account, site string, req api.CreateHostingReleaseRequest) (api.HostingRelease, bool) {
	var release api.HostingRelease
	var err error
	if req.Provenance != nil {
		history, ok := s.historyHost(w, a)
		if !ok {
			return release, false
		}
		release, err = history.CreateHostingReleaseWithProvenance(r.Context(), a.ID, site, req.HostingManifest, req.Provenance)
	} else {
		release, err = s.Host.HostingStore().CreateHostingRelease(r.Context(), a.ID, site, req.HostingManifest)
	}
	if err != nil {
		failure(w, err)
		return release, false
	}
	return release, true
}
