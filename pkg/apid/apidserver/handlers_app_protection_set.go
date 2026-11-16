package apidserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/platformflags"
	"github.com/onebox-faas/faas/pkg/state"
)

// setAppProtection serves PUT /v1/apps/{slug}/protection
// (ADR-20261116120000 §5). on enrolls the app, including one created before
// protection existed, and makes its reconcile due now. off records the
// choice and deletes the baseline objects the customer has not changed;
// changed objects, and the account email channel other apps may share,
// stay because they are now the customer's.
func (s *server) setAppProtection(w http.ResponseWriter, r *http.Request, acct state.Account) {
	var req api.SetAppProtectionRequest
	if err := decodeJSON(r, &req); err != nil {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation, "Bad request", err.Error()))
		return
	}
	if req.State != api.AppProtectionStateOn && req.State != api.AppProtectionStateOff {
		api.WriteProblem(w, api.ErrValidation("state must be on or off"))
		return
	}
	store, ok := s.store.(state.AppProtectionStore)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("app protection is unavailable on this deployment"))
		return
	}
	app, ok := s.loadApp(w, r, acct, r.PathValue("slug")) //nolint:contextcheck // loadApp uses r.Context().
	if !ok {
		return
	}
	enrollment, err := s.changeAppProtection(r.Context(), store, acct, app, req.State, "api")
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not update app protection"))
		return
	}
	records, err := store.ListAppProtectionObjects(r.Context(), app.ID)
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not read app protection"))
		return
	}
	gone, err := s.removedProtectionObjects(r.Context(), enrollment, records)
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not read app protection"))
		return
	}
	writeJSON(w, http.StatusOK, appProtectionResponse(enrollment, true, records, gone))
}

// changeAppProtection records the on or off choice and, for off, deletes
// the unchanged baseline objects. source is api or manifest, for the audit
// event.
func (s *server) changeAppProtection(ctx context.Context, store state.AppProtectionStore, acct state.Account, app state.App, want, source string) (state.AppProtection, error) {
	enrollment, err := store.SetAppProtection(ctx, app.ID, acct.ID, want)
	if err != nil {
		return enrollment, err
	}
	removed := []string{}
	if want == api.AppProtectionStateOff {
		if removed, err = s.removeProtectionObjects(ctx, store, enrollment, app); err != nil {
			return enrollment, err
		}
	}
	s.audit.Emit(ctx, "protection."+want, &acct.ID, map[string]any{
		"app_id": app.ID, "removed": removed, "source": source,
	})
	return enrollment, nil
}

// applyManifestProtection turns protection on or off as a source deploy's
// manifest says (ADR-20261116120000 §5). It does nothing when the key is
// absent, the app is already in that state, or the capability is off for
// the account.
func (s *server) applyManifestProtection(ctx context.Context, acct state.Account, app state.App, want string) *api.Problem {
	if want == "" || !s.platformFlagFor(platformflags.CapabilityFlagKey(protectionBaselineCapability), appFlagSubject(acct, app)) {
		return nil
	}
	store, ok := s.store.(state.AppProtectionStore)
	if !ok {
		return nil
	}
	current, found, err := store.GetAppProtection(ctx, app.ID)
	if err != nil {
		return api.ErrCapacity("could not read app protection")
	}
	if found && current.State == want {
		return nil
	}
	if _, err := s.changeAppProtection(ctx, store, acct, app, want, "manifest"); err != nil {
		s.log.ErrorContext(ctx, "apply manifest protection", "app_id", app.ID, "err", err)
		return api.ErrCapacity("could not apply the manifest's protection key")
	}
	return nil
}

// protectionRemoveOrder deletes dependents first: the alerts and the gate
// watch the SLOs and the check. The email channel is never deleted.
var protectionRemoveOrder = []state.AppProtectionItem{
	state.AppProtectionAvailabilityBurnAlert,
	state.AppProtectionLatencyBurnAlert,
	state.AppProtectionHealthAlert,
	state.AppProtectionAvailabilityGate,
	state.AppProtectionAvailabilitySLO,
	state.AppProtectionLatencySLO,
	state.AppProtectionHealthCheck,
}

// removeProtectionObjects deletes the app's unchanged baseline objects and
// forgets their records, so turning protection on again recreates them.
// Records of objects already gone are forgotten too; skipped records are
// dropped. It returns the items it removed.
func (s *server) removeProtectionObjects(ctx context.Context, store state.AppProtectionStore, enrollment state.AppProtection, app state.App) ([]string, error) {
	records, err := store.ListAppProtectionObjects(ctx, app.ID)
	if err != nil {
		return nil, err
	}
	byItem := map[state.AppProtectionItem]state.AppProtectionObject{}
	for _, rec := range records {
		if rec.Status != state.AppProtectionObjectCreated {
			if err := store.DeleteAppProtectionObject(ctx, app.ID, rec.Item); err != nil {
				return nil, err
			}
			continue
		}
		byItem[rec.Item] = rec
	}
	removed := []string{}
	for _, item := range protectionRemoveOrder {
		rec, ok := byItem[item]
		if !ok {
			continue
		}
		deleted, err := s.deleteUnchangedProtectionObject(ctx, enrollment, app, rec)
		if err != nil {
			s.log.Warn("protection off: delete object", "app", app.ID, "item", item, "err", err)
			continue
		}
		if !deleted {
			continue // changed by the customer: theirs now
		}
		if err := store.DeleteAppProtectionObject(ctx, app.ID, item); err != nil {
			return removed, err
		}
		removed = append(removed, string(item))
	}
	return removed, nil
}

// deleteUnchangedProtectionObject deletes one baseline object when it still
// holds the values the baseline wrote. It reports deleted (or already gone)
// as true and a changed object as false.
func (s *server) deleteUnchangedProtectionObject(ctx context.Context, enrollment state.AppProtection, app state.App, rec state.AppProtectionObject) (bool, error) {
	var written map[string]any
	if err := json.Unmarshal(rec.Written, &written); err != nil {
		return false, err
	}
	switch rec.Item {
	case state.AppProtectionAvailabilitySLO, state.AppProtectionLatencySLO, state.AppProtectionHealthCheck:
		// Deleting an SLO or check deletes the rules that watch it; one the
		// customer kept (or wrote) still needs it.
		inUse, err := s.protectionTargetInUse(ctx, app, rec.ObjectID)
		if err != nil || inUse {
			return false, err
		}
	}
	switch rec.Item {
	case state.AppProtectionAvailabilitySLO, state.AppProtectionLatencySLO:
		store, ok := s.store.(state.SLOStore)
		if !ok {
			return false, errors.New("SLOs unavailable")
		}
		slo, err := store.GetSLO(ctx, app.ID, rec.ObjectID)
		if errors.Is(err, state.ErrNotFound) {
			return true, nil
		}
		if err != nil {
			return false, err
		}
		if !protectionUnchanged(written, map[string]any{"name": slo.Name, "sli": slo.SLI,
			"latency_threshold_ms": slo.LatencyThresholdMS, "objective_bp": slo.ObjectiveBP, "window_days": slo.WindowDays}) {
			return false, nil
		}
		return true, ignoreNotFound(store.DeleteSLO(ctx, app.ID, rec.ObjectID))
	case state.AppProtectionHealthCheck:
		store, ok := s.store.(state.SyntheticCheckStore)
		if !ok {
			return false, errors.New("synthetic checks unavailable")
		}
		check, err := store.GetSyntheticCheck(ctx, app.ID, rec.ObjectID)
		if errors.Is(err, state.ErrNotFound) {
			return true, nil
		}
		if err != nil {
			return false, err
		}
		if !protectionUnchanged(written, map[string]any{"name": check.Name, "method": check.Method, "path": check.Path,
			"interval_seconds": check.IntervalSeconds, "timeout_ms": check.TimeoutMS, "expected_status": check.ExpectedStatus}) {
			return false, nil
		}
		return true, ignoreNotFound(store.DeleteSyntheticCheck(ctx, app.ID, rec.ObjectID))
	case state.AppProtectionAvailabilityBurnAlert, state.AppProtectionLatencyBurnAlert, state.AppProtectionHealthAlert:
		rule, err := s.store.AlertRuleByID(ctx, rec.ObjectID)
		if errors.Is(err, state.ErrNotFound) || (err == nil && rule.AppID != app.ID) {
			return true, nil
		}
		if err != nil {
			return false, err
		}
		if !protectionUnchanged(written, map[string]any{"name": rule.Name, "metric": string(rule.Metric),
			"comparison": string(rule.Comparison), "threshold": rule.Threshold, "window_spec": string(rule.WindowSpec),
			"slo_id": rule.SLOID, "synthetic_check_id": rule.SyntheticCheckID}) {
			return false, nil
		}
		return true, ignoreNotFound(s.store.DeleteAlertRule(ctx, rec.ObjectID))
	case state.AppProtectionAvailabilityGate:
		store, ok := s.store.(state.ReleaseGateStore)
		if !ok {
			return false, errors.New("release gates unavailable")
		}
		gates, err := store.ListReleaseGates(ctx, app.ID)
		if err != nil {
			return false, err
		}
		for _, g := range gates {
			if g.ID != rec.ObjectID {
				continue
			}
			if !protectionUnchanged(written, map[string]any{"name": g.Name, "kind": g.Kind, "slo_id": g.SLOID,
				"max_burn_rate": g.MaxBurnRate, "min_samples": g.MinSamples}) {
				return false, nil
			}
			_, err := store.DeleteReleaseGate(ctx, app.ID, g.Name)
			return true, ignoreNotFound(err)
		}
		return true, nil
	}
	return false, fmt.Errorf("protection: item %s is never removed", rec.Item)
}

// protectionTargetInUse reports whether an alert rule or release gate of the
// app still references the SLO or synthetic check id.
func (s *server) protectionTargetInUse(ctx context.Context, app state.App, id string) (bool, error) {
	rules, err := s.store.ListAlertRulesForAccount(ctx, app.AccountID)
	if err != nil {
		return false, err
	}
	for _, r := range rules {
		if r.AppID == app.ID && (r.SLOID == id || r.SyntheticCheckID == id) {
			return true, nil
		}
	}
	if gates, ok := s.store.(state.ReleaseGateStore); ok {
		list, err := gates.ListReleaseGates(ctx, app.ID)
		if err != nil {
			return false, err
		}
		for _, g := range list {
			if g.SLOID == id || g.SyntheticCheckID == id {
				return true, nil
			}
		}
	}
	return false, nil
}

// protectionUnchanged compares the values the baseline wrote with the
// object's current ones. Written values went through JSON, so both sides
// are compared in their JSON form; a key the baseline did not write is
// ignored.
func protectionUnchanged(written, current map[string]any) bool {
	for key, want := range written {
		got, ok := current[key]
		if !ok {
			continue
		}
		a, errA := json.Marshal(want)
		b, errB := json.Marshal(got)
		if errA != nil || errB != nil || string(a) != string(b) {
			return false
		}
	}
	return true
}

func ignoreNotFound(err error) error {
	if errors.Is(err, state.ErrNotFound) {
		return nil
	}
	return err
}
