package apidserver

import (
	"context"
	"net/http"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/dataapiops"
	"github.com/onebox-faas/faas/pkg/apid/dataapiruntime"
	"github.com/onebox-faas/faas/pkg/dashboard"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *server) populateDataAPIOperations(r *http.Request, acct state.Account, app state.App, data *dashboard.DataAPIData) {
	if r.URL.Query().Get("observe") != "1" {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), api.DataAPIHealthHTTPTimeout)
	defer cancel()
	current := ""
	if runtime, err := dataapiruntime.Current(); err == nil {
		current = runtime.Version
	}
	client := &http.Client{Timeout: api.DataAPIHealthHTTPTimeout, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	report := dataapiops.Probe(ctx, client, appURLForDomain(app.Slug, s.domain), current)
	data.Operations = &report
	requestID := r.URL.Query().Get("request_id")
	if requestID != "" && !dataapiops.ValidRequestID(requestID) {
		report.Message = "Enter a valid request ID."
		return
	}
	data.RequestID = requestID
	// Log reads have their own deadline; a slow public probe must not hide history.
	logCtx, logCancel := context.WithTimeout(r.Context(), api.LogSearchStatementTimeout)
	defer logCancel()
	now := time.Now().UTC()
	events, more, err := s.logEvents().ListLogEvents(logCtx, state.LogEventFilter{AccountID: acct.ID, AppID: app.ID, Source: state.LogEventSourceHTTP, Since: now.Add(-api.LogSearchDefaultWindow), Until: now, RequestID: requestID, Limit: state.MaxLogEventPage, Timeout: api.LogSearchStatementTimeout})
	if err != nil {
		report.Message = "Recent request history is unavailable."
	} else {
		report.Sample = dataapiops.Summarize(events, more)
	}
	if s.managedPostgresBindings != nil {
		if budget, err := s.managedPostgresBindings.AppConnectionBudgets(r.Context(), acct.ID, app.ID, app.Slug); err == nil {
			report.Budgets = &budget
		}
	}
}
