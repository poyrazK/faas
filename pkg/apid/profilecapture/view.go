package profilecapture

import (
	"context"
	"fmt"
	"net/http"
	"slices"

	"github.com/google/pprof/profile"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/httpkit"
	"github.com/onebox-faas/faas/pkg/profiling"
	"github.com/onebox-faas/faas/pkg/state"
)

// merged loads a ready capture and merges one kind across its processes.
// Parsing happens here, outside vmmd and schedd, with the same bounds as
// continuous profile queries.
func (s *Service) merged(w http.ResponseWriter, r *http.Request, acct state.Account) (api.ProfileCapture, string, *profile.Profile, bool) {
	app, store, ok := s.Target(w, r, acct)
	if !ok {
		return api.ProfileCapture{}, "", nil, false
	}
	capture, ok := s.Owned(w, r, acct, app, store)
	if !ok {
		return capture, "", nil, false
	}
	kind := r.URL.Query().Get("kind")
	if kind == "" && len(capture.Kinds) > 0 {
		kind = capture.Kinds[0]
	}
	if !slices.Contains(capture.Kinds, kind) {
		api.WriteProblem(w, api.ErrValidation("kind must be one of the capture's kinds"))
		return capture, "", nil, false
	}
	if capture.Status != api.ProfileCaptureReady {
		api.WriteProblem(w, api.NewProblem(http.StatusConflict, api.CodeConflict, "Capture not ready", fmt.Sprintf("profile capture is %s", capture.Status)))
		return capture, "", nil, false
	}
	blobs, err := store.ProfileCaptureBlobs(r.Context(), acct.ID, app.ID, capture.ID)
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("profile capture data is unavailable"))
		return capture, "", nil, false
	}
	merged, _, err := profiling.MergeCapture(KindProfiles(blobs, kind), kind)
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("profile capture could not be merged"))
		return capture, "", nil, false
	}
	return capture, kind, merged, true
}

// KindProfiles returns the raw per-process profiles of one kind.
func KindProfiles(blobs []state.ProfileCaptureBlob, kind string) [][]byte {
	var raws [][]byte
	for _, b := range blobs {
		if b.Kind == kind {
			raws = append(raws, b.Profile)
		}
	}
	return raws
}

// View handles GET /v1/apps/{slug}/profiles/captures/{id}/view.
func (s *Service) View(w http.ResponseWriter, r *http.Request, acct state.Account) {
	capture, kind, merged, ok := s.merged(w, r, acct)
	if !ok {
		return
	}
	view, err := profiling.CaptureView(merged, kind)
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("profile exceeds supported view bounds; download the pprof file instead"))
		return
	}
	view.Capture = capture
	s.LinkSources(r.Context(), acct, capture, &view)
	httpkit.WriteJSON(w, http.StatusOK, view)
}

// LinkSources links frames to the capture deployment's recorded commit,
// exactly like continuous profiles. Captures without a deployment (failed
// before reaching an instance) keep plain symbols.
func (s *Service) LinkSources(ctx context.Context, acct state.Account, capture api.ProfileCapture, view *api.ProfileCaptureView) {
	if capture.DeploymentID == "" {
		return
	}
	store := s.Host.Store()
	app, err := store.AppByID(ctx, capture.AppID)
	if err != nil || app.AccountID != acct.ID {
		return
	}
	dep, err := store.DeploymentByID(ctx, capture.DeploymentID)
	if err != nil || dep.AppID != app.ID {
		return
	}
	profiling.LinkCaptureSources(view, s.Host.SourceProvenance(app, dep), app.Runtime)
}

// Download handles GET /v1/apps/{slug}/profiles/captures/{id}/pprof: the
// merged profile as gzip pprof for go tool pprof, speedscope or Pyroscope.
func (s *Service) Download(w http.ResponseWriter, r *http.Request, acct state.Account) {
	capture, kind, merged, ok := s.merged(w, r, acct)
	if !ok {
		return
	}
	if merged == nil {
		api.WriteProblem(w, api.NewProblem(http.StatusNotFound, api.CodeNotFound, "Profile not captured", "the capture has no "+kind+" profile"))
		return
	}
	body, err := profiling.EncodeCapture(merged)
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("profile could not be encoded"))
		return
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", "profile-"+capture.ID+"-"+kind+".pb.gz"))
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(body)
}
