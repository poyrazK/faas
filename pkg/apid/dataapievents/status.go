package dataapievents

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/dataapiworkload"
	"github.com/onebox-faas/faas/pkg/state/dataapiusage"
	"github.com/onebox-faas/faas/pkg/workloadidentity"
)

// ReportStore keeps the change hook delivery each runtime last reported;
// *dataapiusage.Store implements it.
type ReportStore interface {
	RecordChangeHooks(ctx context.Context, accountID, appID string, report api.DataAPIChangeHookReport, now time.Time) error
}

// StatusHandler accepts a runtime's change hook delivery report
// (ADR-20261022121642 amendment 1) under the same workload identity as its
// publications: a gregale:data-api:events token from a live instance of an
// active app, reporting for that app only.
type StatusHandler struct {
	Verifier *workloadidentity.Verifier
	Store    Store
	Reports  ReportStore
	Now      func() time.Time
}

func (h StatusHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	now := time.Now()
	if h.Now != nil {
		now = h.Now()
	}
	_, app, err := dataapiworkload.Live(r, h.Verifier, workloadidentity.DataAPIEventsAudience, h.Store, now)
	if err != nil {
		dataapiworkload.Refuse(w, err, "a gregale:data-api:events workload token from a live instance of an active app is required")
		return
	}
	if h.Reports == nil {
		api.WriteProblem(w, api.ErrCapacity("change hook reports are unavailable"))
		return
	}
	var report api.DataAPIChangeHookReport
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, api.DataAPIChangeHookReportMaxBytes))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&report) != nil || !errors.Is(decoder.Decode(new(any)), io.EOF) {
		api.WriteProblem(w, api.ErrValidation("the body must be one version 1 change hook report"))
		return
	}
	if err := report.Validate(now); err != nil {
		api.WriteProblem(w, api.ErrValidation(err.Error()))
		return
	}
	// The app's own account, not its organization's: reports are keyed by
	// the app row, which records the account that created it.
	if err := h.Reports.RecordChangeHooks(r.Context(), app.AccountID, app.ID, report, now); err != nil {
		if errors.Is(err, dataapiusage.ErrNotFound) {
			api.WriteProblem(w, api.NewProblem(http.StatusUnauthorized, api.CodeUnauthorized, "Workload identity required", "the token's app is gone"))
			return
		}
		slog.Error("data api change hook report", "app_id", app.ID, "err", err)
		api.WriteProblem(w, api.ErrCapacity("change hook reports are unavailable"))
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
