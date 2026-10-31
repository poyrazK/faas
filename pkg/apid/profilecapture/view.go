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

// requestKind resolves ?kind= for a capture, defaulting to its first kind.
func requestKind(r *http.Request, capture api.ProfileCapture) string {
	if kind := r.URL.Query().Get("kind"); kind != "" {
		return kind
	}
	if len(capture.Kinds) > 0 {
		return capture.Kinds[0]
	}
	return ""
}

// mergeKind merges one kind of a ready capture across its processes.
// Parsing happens here, outside vmmd and schedd, with the same bounds as
// continuous profile queries.
func mergeKind(ctx context.Context, acct state.Account, app state.App, store state.ProfileCaptureStore, capture api.ProfileCapture, kind string) (*profile.Profile, *api.Problem) {
	if !slices.Contains(capture.Kinds, kind) {
		return nil, api.ErrValidation("kind must be one of the capture's kinds")
	}
	if capture.Status != api.ProfileCaptureReady {
		return nil, api.NewProblem(http.StatusConflict, api.CodeConflict, "Capture not ready", fmt.Sprintf("profile capture is %s", capture.Status))
	}
	blobs, err := store.ProfileCaptureBlobs(ctx, acct.ID, app.ID, capture.ID)
	if err != nil {
		return nil, api.ErrCapacity("profile capture data is unavailable")
	}
	merged, _, err := profiling.MergeCapture(KindProfiles(blobs, kind), kind)
	if err != nil {
		return nil, api.ErrCapacity("profile capture could not be merged")
	}
	return merged, nil
}

// KindView is one kind of a ready capture as a source-linked view.
func (s *Service) KindView(ctx context.Context, acct state.Account, app state.App, store state.ProfileCaptureStore, capture api.ProfileCapture, kind string) (api.ProfileCaptureView, *api.Problem) {
	merged, problem := mergeKind(ctx, acct, app, store, capture, kind)
	if problem != nil {
		return api.ProfileCaptureView{}, problem
	}
	view, err := profiling.CaptureView(merged, kind)
	if err != nil {
		return view, api.ErrCapacity("profile exceeds supported view bounds; download the pprof file instead")
	}
	view.Capture = capture
	s.LinkSources(ctx, acct, capture, &view)
	return view, nil
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
	app, store, ok := s.Target(w, r, acct)
	if !ok {
		return
	}
	capture, ok := s.Owned(w, r, acct, app, store)
	if !ok {
		return
	}
	view, problem := s.KindView(r.Context(), acct, app, store, capture, requestKind(r, capture))
	if problem != nil {
		api.WriteProblemForRequest(w, r, problem)
		return
	}
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
	app, store, ok := s.Target(w, r, acct)
	if !ok {
		return
	}
	capture, ok := s.Owned(w, r, acct, app, store)
	if !ok {
		return
	}
	kind := requestKind(r, capture)
	merged, problem := mergeKind(r.Context(), acct, app, store, capture, kind)
	if problem != nil {
		api.WriteProblemForRequest(w, r, problem)
		return
	}
	if merged == nil {
		api.WriteProblemForRequest(w, r, api.NewProblem(http.StatusNotFound, api.CodeNotFound, "Profile not captured", "the capture has no "+kind+" profile"))
		return
	}
	body, err := profiling.EncodeCapture(merged)
	if err != nil {
		api.WriteProblemForRequest(w, r, api.ErrCapacity("profile could not be encoded"))
		return
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", "profile-"+capture.ID+"-"+kind+".pb.gz"))
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(body)
}
