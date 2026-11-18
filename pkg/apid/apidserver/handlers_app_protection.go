package apidserver

import (
	"context"
	"errors"
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
	if !enrolled {
		writeJSON(w, http.StatusOK, appProtectionResponse(enrollment, false, nil, nil))
		return
	}
	records, err := store.ListAppProtectionObjects(r.Context(), app.ID)
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not read app protection"))
		return
	}
	removed, err := s.removedProtectionObjects(r.Context(), enrollment, records)
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not read app protection"))
		return
	}
	writeJSON(w, http.StatusOK, appProtectionResponse(enrollment, true, records, removed))
}

// appProtectionResponse builds the status: every baseline item in order.
// A created item is active, or removed when the customer deleted the
// object; a skipped item carries its reason; an unrecorded one is pending.
func appProtectionResponse(enrollment state.AppProtection, enrolled bool, records []state.AppProtectionObject,
	removed map[state.AppProtectionItem]bool) api.AppProtectionResponse {
	if !enrolled {
		return api.AppProtectionResponse{State: api.AppProtectionStateNone, Items: []api.AppProtectionItemStatus{}}
	}
	byItem := make(map[string]state.AppProtectionObject, len(records))
	for _, rec := range records {
		byItem[string(rec.Item)] = rec
	}
	out := api.AppProtectionResponse{
		State: enrollment.State,
		Items: make([]api.AppProtectionItemStatus, 0, len(api.ProtectionBaselineItems)),
	}
	out.Protected = enrollment.State == state.AppProtectionOn
	for _, item := range api.ProtectionBaselineItems {
		status := api.AppProtectionItemStatus{Item: item.Item, Kind: item.Kind, Name: item.Name, State: api.AppProtectionItemPending}
		if rec, ok := byItem[item.Item]; ok {
			switch {
			case rec.Status == state.AppProtectionObjectSkipped:
				status.State, status.Reason = api.AppProtectionItemSkipped, rec.Reason
			case removed[rec.Item]:
				status.State, status.ObjectID = api.AppProtectionItemRemoved, rec.ObjectID
			default:
				status.State, status.ObjectID = api.AppProtectionItemActive, rec.ObjectID
			}
		}
		if status.State != api.AppProtectionItemActive {
			out.Protected = false
		}
		out.Items = append(out.Items, status)
	}
	return out
}

// removedProtectionObjects reports which created baseline objects no longer
// exist: the customer deleted them.
func (s *server) removedProtectionObjects(ctx context.Context, enrollment state.AppProtection,
	records []state.AppProtectionObject) (map[state.AppProtectionItem]bool, error) {
	removed := map[state.AppProtectionItem]bool{}
	var gateIDs map[string]bool
	for _, rec := range records {
		if rec.Status != state.AppProtectionObjectCreated {
			continue
		}
		exists, err := s.protectionObjectExists(ctx, enrollment, rec, &gateIDs)
		if err != nil {
			return nil, err
		}
		if !exists {
			removed[rec.Item] = true
		}
	}
	return removed, nil
}

// protectionObjectExists looks one created object up by kind. gateIDs
// caches the app's release gate ids across calls.
func (s *server) protectionObjectExists(ctx context.Context, enrollment state.AppProtection, rec state.AppProtectionObject,
	gateIDs *map[string]bool) (bool, error) {
	var err error
	switch rec.Item {
	case state.AppProtectionAvailabilitySLO, state.AppProtectionLatencySLO:
		store, ok := s.store.(state.SLOStore)
		if !ok {
			return true, nil
		}
		_, err = store.GetSLO(ctx, rec.AppID, rec.ObjectID)
	case state.AppProtectionEmailChannel:
		store, ok := s.store.(state.NotificationChannelStore)
		if !ok {
			return true, nil
		}
		_, err = store.GetNotificationChannel(ctx, enrollment.AccountID, rec.ObjectID)
	case state.AppProtectionAvailabilityBurnAlert, state.AppProtectionLatencyBurnAlert, state.AppProtectionHealthAlert:
		var rule state.AlertRule
		rule, err = s.store.AlertRuleByID(ctx, rec.ObjectID)
		if err == nil && rule.AppID != rec.AppID {
			return false, nil
		}
	case state.AppProtectionAvailabilityGate:
		if *gateIDs == nil {
			store, ok := s.store.(state.ReleaseGateStore)
			if !ok {
				return true, nil
			}
			gates, err := store.ListReleaseGates(ctx, rec.AppID)
			if err != nil {
				return false, err
			}
			*gateIDs = make(map[string]bool, len(gates))
			for _, g := range gates {
				(*gateIDs)[g.ID] = true
			}
		}
		return (*gateIDs)[rec.ObjectID], nil
	default:
		return true, nil
	}
	if errors.Is(err, state.ErrNotFound) {
		return false, nil
	}
	return err == nil, err
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
