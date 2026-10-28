package profilecapture

import (
	"context"
	"net/http"
	"slices"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/httpkit"
	"github.com/onebox-faas/faas/pkg/profiling"
	"github.com/onebox-faas/faas/pkg/state"
)

// Diff handles GET /v1/apps/{slug}/profiles/captures/{id}/diff?base=&kind=:
// the capture against an earlier one of the same app, typically to see
// which call paths retained more memory (ADR-1155).
func (s *Service) Diff(w http.ResponseWriter, r *http.Request, acct state.Account) {
	app, store, ok := s.Target(w, r, acct)
	if !ok {
		return
	}
	cand, ok := s.Owned(w, r, acct, app, store)
	if !ok {
		return
	}
	diff, problem := s.Compare(r.Context(), acct, app, store, cand, r.URL.Query().Get("base"), r.URL.Query().Get("kind"))
	if problem != nil {
		api.WriteProblemForRequest(w, r, problem)
		return
	}
	httpkit.WriteJSON(w, http.StatusOK, diff)
}

// Compare diffs one kind of cand against the app's capture baseID. An empty
// kind picks a kind both captures collected, preferring heap.
func (s *Service) Compare(ctx context.Context, acct state.Account, app state.App, store state.ProfileCaptureStore, cand api.ProfileCapture, baseID, kind string) (api.ProfileCaptureDiff, *api.Problem) {
	if baseID == "" || baseID == cand.ID {
		return api.ProfileCaptureDiff{}, api.ErrValidation("base must name another capture of this app")
	}
	base, problem := load(ctx, acct, app, store, baseID)
	if problem != nil {
		if problem.Status == http.StatusNotFound {
			return api.ProfileCaptureDiff{}, api.ErrValidation("base must name another capture of this app")
		}
		return api.ProfileCaptureDiff{}, problem
	}
	if kind == "" {
		kind = CommonKind(base, cand)
	}
	if kind == "" || !slices.Contains(base.Kinds, kind) || !slices.Contains(cand.Kinds, kind) {
		return api.ProfileCaptureDiff{}, api.ErrValidation("both captures must include the compared kind")
	}
	baseView, problem := s.KindView(ctx, acct, app, store, base, kind)
	if problem != nil {
		return api.ProfileCaptureDiff{}, problem
	}
	candView, problem := s.KindView(ctx, acct, app, store, cand, kind)
	if problem != nil {
		return api.ProfileCaptureDiff{}, problem
	}
	diff, err := profiling.DiffCaptureViews(baseView, candView)
	if err != nil {
		s.Host.Logger().Error("apid: compare profile captures", "base", base.ID, "candidate", cand.ID, "err", err)
		return api.ProfileCaptureDiff{}, api.ErrCapacity("The captures could not be compared.")
	}
	return diff, nil
}

// CommonKind returns a kind both captures collected, preferring heap
// because comparing captures is mostly about memory growth, then CPU, then
// lock contention.
func CommonKind(a, b api.ProfileCapture) string {
	for _, kind := range []string{api.ProfileKindHeap, api.ProfileKindCPU, api.ProfileKindMutex, api.ProfileKindBlock} {
		if slices.Contains(a.Kinds, kind) && slices.Contains(b.Kinds, kind) {
			return kind
		}
	}
	return ""
}

// Comparable lists the app's other ready captures that share a kind with
// capture, newest first, for a compare picker.
func Comparable(capture api.ProfileCapture, all []api.ProfileCapture) []api.ProfileCapture {
	out := []api.ProfileCapture{}
	for _, c := range all {
		if c.ID != capture.ID && c.Status == api.ProfileCaptureReady && CommonKind(c, capture) != "" {
			out = append(out, c)
		}
	}
	return out
}
