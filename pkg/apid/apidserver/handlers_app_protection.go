package apidserver

import (
	"context"
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/platformflags"
	"github.com/onebox-faas/faas/pkg/state"
)

// protectionBaselineCapability is the product capability (and, through it,
// the platform flag) that releases the protection baseline
// (ADR-20261116120000).
const protectionBaselineCapability = "protection-baseline"

// getAppProtection serves GET /v1/apps/{slug}/protection: the app's
// protection-baseline enrollment and the state of each baseline item.
func (s *server) getAppProtection(w http.ResponseWriter, r *http.Request, acct state.Account) {
	store, ok := s.store.(state.AppProtectionStore)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("app protection is unavailable on this deployment"))
		return
	}
	app, ok := s.loadApp(w, r, acct, r.PathValue("slug")) //nolint:contextcheck // loadApp uses r.Context().
	if !ok {
		return
	}
	enrollment, enrolled, err := store.GetAppProtection(r.Context(), app.ID)
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not read app protection"))
		return
	}
	var objects []state.AppProtectionObject
	if enrolled {
		if objects, err = store.ListAppProtectionObjects(r.Context(), app.ID); err != nil {
			api.WriteProblem(w, api.ErrCapacity("could not read app protection"))
			return
		}
	}
	writeJSON(w, http.StatusOK, appProtectionResponse(enrollment, enrolled, objects))
}

// appProtectionResponse builds the status: every baseline item in order,
// active when the baseline recorded an object for it, pending otherwise.
func appProtectionResponse(enrollment state.AppProtection, enrolled bool, objects []state.AppProtectionObject) api.AppProtectionResponse {
	if !enrolled {
		return api.AppProtectionResponse{State: api.AppProtectionStateNone, Items: []api.AppProtectionItemStatus{}}
	}
	recorded := make(map[string]string, len(objects))
	for _, obj := range objects {
		recorded[string(obj.Item)] = obj.ObjectID
	}
	out := api.AppProtectionResponse{
		State: enrollment.State,
		Items: make([]api.AppProtectionItemStatus, 0, len(api.ProtectionBaselineItems)),
	}
	out.Protected = enrollment.State == state.AppProtectionOn
	for _, item := range api.ProtectionBaselineItems {
		status := api.AppProtectionItemStatus{Item: item.Item, Kind: item.Kind, Name: item.Name, State: api.AppProtectionItemPending}
		if id, ok := recorded[item.Item]; ok {
			status.State = api.AppProtectionItemActive
			status.ObjectID = id
		} else {
			out.Protected = false
		}
		out.Items = append(out.Items, status)
	}
	return out
}

// enrollNewAppInProtection enrolls an app created while the protection
// baseline is released to its account (ADR-20261116120000 §1). Apps created
// before the release are never enrolled here. Enrollment is best effort: a
// failure is logged and never fails the app creation.
func (s *server) enrollNewAppInProtection(ctx context.Context, acct state.Account, app state.App) {
	store, ok := s.store.(state.AppProtectionStore)
	if !ok {
		return
	}
	if !s.platformFlagFor(platformflags.CapabilityFlagKey(protectionBaselineCapability), appFlagSubject(acct, app)) {
		return
	}
	if _, err := store.SetAppProtection(ctx, app.ID, acct.ID, state.AppProtectionOn); err != nil {
		if s.log != nil {
			s.log.Warn("protection: enroll new app", "app", app.ID, "err", err)
		}
		return
	}
	s.audit.Emit(ctx, "protection.enrolled", &acct.ID, map[string]any{"app_id": app.ID, "source": "app_created"})
}
