package apidserver

import (
	"errors"
	"net/http"
	"time"
	"unicode/utf8"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/insights"
	"github.com/onebox-faas/faas/pkg/flags"
	"github.com/onebox-faas/faas/pkg/platformflags"
	"github.com/onebox-faas/faas/pkg/state"
)

// runtimeFeatureFlagEvents is POST /v1/runtime/flags/events: a runtime SDK
// reports conversion events for experiments with the workload identity it
// uses to read flags (ADR 20261115163113 §2). The token decides account,
// app and environment; the body never does.
func (s *server) runtimeFeatureFlagEvents(w http.ResponseWriter, r *http.Request) {
	if !s.platformFlagOn(platformflags.CustomerFlags) {
		api.WriteProblem(w, api.NewProblem(http.StatusForbidden, api.CodeForbidden, "Flags unavailable", "feature flags require operator qualification"))
		return
	}
	scope, err := s.runtimeFeatureFlagScope(r)
	if errors.Is(err, errFlagsNotReleased) {
		api.WriteProblem(w, api.NewProblem(http.StatusForbidden, api.CodeForbidden, "Flags unavailable", "feature flags are not enabled for this account"))
		return
	}
	if err != nil {
		api.WriteProblem(w, api.NewProblem(http.StatusUnauthorized, api.CodeUnauthorized, "Workload identity required", "a live workload token for gregale:flags is required"))
		return
	}
	store, ok := s.store.(state.FlagEventStore)
	if !ok {
		api.WriteProblem(w, api.ErrInternal("flag events unavailable"))
		return
	}
	var req api.FlagEventsRequest
	if !decodeJSONLimit(w, r, &req, api.FlagEventsMaxBodyBytes) {
		return
	}
	if len(req.Events) > api.FlagEventsMaxBatch || len(req.Events)+len(req.Exposures) == 0 {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation, "Invalid event batch",
			"a batch carries 1 to 100 events, or exposures only").WithLimit(api.FlagEventsMaxBatch, int64(len(req.Events))))
		return
	}
	if len(req.Exposures) > api.FlagExposuresPerRequest {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation, "Invalid exposure batch",
			"a batch carries at most 100 exposures").WithLimit(api.FlagExposuresPerRequest, int64(len(req.Exposures))))
		return
	}
	acct, err := s.store.AccountByID(r.Context(), scope.AccountID)
	if err != nil {
		api.WriteProblem(w, api.ErrInternal("flag events unavailable"))
		return
	}
	now := time.Now().UTC()
	events, invalid := validFlagEvents(req.Events, now)
	quota := api.Plan(acct.Plan).FlagEventsPerMonth()
	analytics := api.Plan(acct.Plan).AnalyticsRetentionDays() > 0 &&
		s.platformFlagFor(platformflags.ProductAnalytics, platformflags.Subject{AccountID: acct.ID, Plan: string(acct.Plan)})
	res, err := store.IngestFlagEvents(r.Context(), state.FlagEventIngest{
		Scope: scope, Quota: quota, MaxNames: api.FlagEventNamesMaxPerEnvironment, Events: events, Now: now,
		Analytics: analytics, MaxPropertyKeys: api.AnalyticsPropertyKeysMaxPerEnvironment})
	if err != nil {
		s.log.Error("flag events ingest", "err", err, "environment", scope.EnvironmentID)
		api.WriteProblem(w, api.ErrInternal("flag events unavailable"))
		return
	}
	exposures, err := s.ingestFlagExposures(r, scope, analytics, req.Exposures, now)
	if err != nil {
		s.log.Error("flag exposures ingest", "err", err, "environment", scope.EnvironmentID)
		api.WriteProblem(w, api.ErrInternal("flag events unavailable"))
		return
	}
	writeJSON(w, http.StatusOK, api.FlagEventsResponse{
		Accepted: res.Accepted, Exposures: exposures,
		Dropped: api.FlagEventsDropped{Quota: res.DroppedQuota, UnknownCustomer: res.DroppedUnknownCustomer, NameLimit: res.DroppedNameLimit, Invalid: invalid},
		Month:   api.FlagEventsMonth{Limit: quota, Accepted: res.MonthAccepted, Dropped: res.MonthDropped},
	})
}

// ingestFlagExposures records subject experiment exposures (ADR
// 20261119173702 §1) when product analytics is on: only it holds the
// subject events their conversions come from. Invalid exposures, those of
// unknown customers and those on no running subject experiment are left
// out of the count.
func (s *server) ingestFlagExposures(r *http.Request, scope state.FeatureFlagScope, analytics bool, in []api.FlagExposure, now time.Time) (int, error) {
	store, ok := s.store.(state.FlagExperimentExtrasStore)
	if !ok || !analytics || len(in) == 0 {
		return 0, nil
	}
	out := make([]state.FlagExperimentExposure, 0, len(in))
	for _, e := range in {
		at := e.At
		if at.IsZero() {
			at = now
		}
		if !flags.ValidKey(e.Flag) || e.RuleID == "" || len(e.RuleID) > 64 || e.Variant == "" || len(e.Variant) > 128 ||
			!flags.ValidSubjectID(e.Subject) || at.Before(now.Add(-api.FlagEventMaxAge)) || at.After(now.Add(api.FlagEventMaxSkew)) {
			continue
		}
		out = append(out, state.FlagExperimentExposure{Flag: e.Flag, RuleID: e.RuleID, Variant: e.Variant, CustomerID: e.CustomerID,
			SubjectHash: state.AnalyticsSubjectHash(scope.EnvironmentID, e.Subject), At: at.UTC()})
	}
	return store.RecordFlagExperimentExposures(r.Context(), scope.AccountID, scope.EnvironmentID, out)
}

// validAnalyticsFields checks an event's product analytics fields
// (ADR 20261117084505 §1). An event with any invalid one is dropped whole,
// so a malformed SDK is visible in the invalid count.
func validAnalyticsFields(e api.FlagEvent) bool {
	if len(e.Subject) > api.AnalyticsSubjectMaxBytes || len(e.Properties) > api.AnalyticsEventMaxProperties || len(e.Flags) > api.AnalyticsEventMaxFlags {
		return false
	}
	if e.Subject != "" && !flags.ValidSubjectID(e.Subject) {
		return false
	}
	for k, v := range e.Properties {
		if !insights.ValidPropertyKey(k) || len(v) > api.AnalyticsPropertyValueMaxBytes || !utf8.ValidString(v) {
			return false
		}
	}
	for k, v := range e.Flags {
		if !flags.ValidKey(k) || len(v) > api.AnalyticsPropertyValueMaxBytes || !utf8.ValidString(v) {
			return false
		}
	}
	return true
}

// validFlagEvents keeps events with a valid name and value whose time is
// within FlagEventMaxAge before now and FlagEventMaxSkew after it; an event
// without a time happened now.
func validFlagEvents(in []api.FlagEvent, now time.Time) ([]state.FlagConversionEvent, int) {
	out := make([]state.FlagConversionEvent, 0, len(in))
	invalid := 0
	for _, e := range in {
		at := e.At
		if at.IsZero() {
			at = now
		}
		if !flags.ValidEventName(e.Name) || e.CustomerID == "" || e.Value > api.FlagEventMaxAbsValue || e.Value < -api.FlagEventMaxAbsValue ||
			at.Before(now.Add(-api.FlagEventMaxAge)) || at.After(now.Add(api.FlagEventMaxSkew)) || !validAnalyticsFields(e) {
			invalid++
			continue
		}
		out = append(out, state.FlagConversionEvent{Name: e.Name, CustomerID: e.CustomerID, Value: e.Value, At: at.UTC(),
			Subject: e.Subject, Properties: e.Properties, Flags: e.Flags})
	}
	return out, invalid
}
