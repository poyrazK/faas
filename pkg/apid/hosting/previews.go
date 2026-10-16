package hosting

import (
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/httpkit"
	"github.com/onebox-faas/faas/pkg/state"
)

type PreviewHost interface {
	HostingPreviewsEnabled() bool
	HostingPreviewStore() state.HostingPreviewStore
}

func (s Service) RequirePreviews(next httpkit.AccountHandler) httpkit.AccountHandler {
	return func(w http.ResponseWriter, r *http.Request, a state.Account) {
		if _, ok := s.previewHost(w, a); ok {
			next(w, r, a)
		}
	}
}
func (s Service) previewHost(w http.ResponseWriter, a state.Account) (PreviewHost, bool) {
	if !s.guard(w, a) {
		return nil, false
	}
	h, ok := s.Host.(PreviewHost)
	if !ok || !h.HostingPreviewsEnabled() || !s.Host.HostingDurableRoutingEnabled() {
		api.WriteProblem(w, api.NewProblem(503, "frontend_hosting_previews_disabled", "Previews unavailable", "private previews require enabled preview and durable routing flags"))
		return nil, false
	}
	if h.HostingPreviewStore() == nil {
		failure(w, state.ErrInvalidArgument)
		return nil, false
	}
	return h, true
}
func (s Service) ListPreviews(w http.ResponseWriter, r *http.Request, a state.Account) {
	h, ok := s.previewHost(w, a)
	if !ok {
		return
	}
	site, ok := s.site(w, r, a)
	if !ok {
		return
	}
	rows, err := h.HostingPreviewStore().ListHostingPreviews(r.Context(), a.ID, site.ID)
	if err != nil {
		failure(w, err)
		return
	}
	httpkit.WriteJSON(w, 200, rows)
}
func (s Service) CreatePreview(w http.ResponseWriter, r *http.Request, a state.Account) {
	h, ok := s.previewHost(w, a)
	if !ok {
		return
	}
	site, ok := s.site(w, r, a)
	if !ok {
		return
	}
	var req api.CreateHostingPreviewRequest
	if httpkit.DecodeJSON(r, &req) != nil || uuid.Validate(req.ReleaseID) != nil {
		api.WriteProblem(w, api.ErrValidation("release_id and a valid ttl_seconds are required"))
		return
	}
	ttl := req.TTLSeconds
	if ttl == 0 {
		ttl = int64(api.HostingPreviewTTLDefault / time.Second)
	}
	if ttl < int64(api.HostingPreviewTTLMin/time.Second) || ttl > int64(api.HostingPreviewTTLMax/time.Second) {
		api.WriteProblem(w, api.ErrValidation("preview TTL is outside the advertised bounds"))
		return
	}
	id := uuid.NewString()
	now := time.Now().UTC()
	p, err := h.HostingPreviewStore().CreateHostingPreview(r.Context(), api.HostingPreview{ID: id, AccountID: a.ID, SiteID: site.ID, ReleaseID: req.ReleaseID, Hostname: "p-" + id + "." + s.Host.HostingDomain(), OriginID: site.OriginID, CreatedAt: now, ExpiresAt: now.Add(time.Duration(ttl) * time.Second)})
	if err != nil {
		failure(w, err)
		return
	}
	s.Host.HostingAudit(r.Context(), a.ID, "hosting_preview_created", map[string]any{"site_id": site.ID, "preview_id": p.ID, "release_id": p.ReleaseID})
	httpkit.WriteJSON(w, http.StatusAccepted, p)
}
func (s Service) RevokePreview(w http.ResponseWriter, r *http.Request, a state.Account) {
	h, ok := s.previewHost(w, a)
	if !ok {
		return
	}
	site, ok := s.site(w, r, a)
	if !ok {
		return
	}
	var req api.RevokeHostingPreviewRequest
	if uuid.Validate(r.PathValue("preview")) != nil || httpkit.DecodeJSON(r, &req) != nil || req.ExpectedGeneration != 1 {
		api.WriteProblem(w, api.ErrValidation("preview and expected_generation=1 are required"))
		return
	}
	p, err := h.HostingPreviewStore().RevokeHostingPreview(r.Context(), a.ID, site.ID, r.PathValue("preview"), req.ExpectedGeneration)
	if err != nil {
		failure(w, err)
		return
	}
	s.Host.HostingAudit(r.Context(), a.ID, "hosting_preview_revoked", map[string]any{"site_id": site.ID, "preview_id": p.ID})
	httpkit.WriteJSON(w, http.StatusAccepted, p)
}
