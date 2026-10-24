// adr:20261019152416 — outboundd probes enforced candidates' outbound bindings itself.
package outbound

import (
	"context"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	oteltrace "go.opentelemetry.io/otel/trace"

	"github.com/onebox-faas/faas/pkg/api"
)

const (
	qualificationProbeBatch   = 50
	qualificationProbeTimeout = 10 * time.Second
	// A failed probe waits before it runs again, so a broken provider
	// credential does not spend the binding's daily limit while the
	// candidate's lease is live.
	qualificationProbeRetry = 30 * time.Second
)

// qualificationPrincipal marks outboundd's own probe of an enforced
// candidate's binding. Like the replay and internal principals, it travels
// only in an in-process context value; no request can carry it.
type qualificationPrincipal struct {
	accountID string
	appID     string
	requestID string
	method    string
	path      string
}

type qualificationPrincipalKey struct{}

func qualificationPrincipalFromContext(ctx context.Context) (qualificationPrincipal, bool) {
	principal, ok := ctx.Value(qualificationPrincipalKey{}).(qualificationPrincipal)
	return principal, ok
}

// qualificationAllowed admits only the probe the binding configured, for an
// app attached to the account's own managed integration. The route is then
// checked against the app's binding like the app's own calls.
func qualificationAllowed(integration Integration, principal qualificationPrincipal, method, path string) bool {
	return integration.AccountID == principal.accountID && integration.ProviderAuthMode == ProviderAuthManaged &&
		integration.AllowsApp(principal.appID) && method == principal.method && path == principal.path
}

// QualificationProbeStore lists probe-ready outbound bindings of enforced
// candidates and records receipts; state.PgStore implements it.
type QualificationProbeStore interface {
	PendingEnvironmentQualificationOutboundProbes(ctx context.Context, limit int) ([]api.OutboundQualificationProbe, error)
	RecordEnvironmentQualificationOutboundReceipt(ctx context.Context, probe api.OutboundQualificationProbe, status int) error
}

// QualificationProbeWorker sends each pending probe through Handler in
// process and records a receipt when it returns the expected status.
type QualificationProbeWorker struct {
	Store    QualificationProbeStore
	Handler  *Handler
	Log      *slog.Logger
	Interval time.Duration
	Now      func() time.Time
	retryAt  map[string]time.Time
}

func (w *QualificationProbeWorker) Run(ctx context.Context) {
	interval := w.Interval
	if interval <= 0 {
		interval = 2 * time.Second
	}
	for {
		if err := w.ProcessPending(ctx); err != nil && ctx.Err() == nil && w.Log != nil {
			w.Log.Warn("outboundd: candidate outbound probes", "err", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(interval):
		}
	}
}

// ProcessPending probes one batch of pending bindings once.
func (w *QualificationProbeWorker) ProcessPending(ctx context.Context) error {
	probes, err := w.Store.PendingEnvironmentQualificationOutboundProbes(ctx, qualificationProbeBatch)
	if err != nil {
		return err
	}
	now := w.now()
	if w.retryAt == nil {
		w.retryAt = map[string]time.Time{}
	}
	for key, at := range w.retryAt {
		if now.After(at.Add(10 * time.Minute)) {
			delete(w.retryAt, key)
		}
	}
	for _, probe := range probes {
		key := probe.RequestID + "/" + strconv.FormatInt(probe.Attempt, 10) + "/" + probe.IntegrationID + "/" + probe.Revision
		if at, waiting := w.retryAt[key]; waiting && now.Before(at) {
			continue
		}
		status := w.Handler.qualificationProbe(ctx, probe)
		if status != probe.Policy.ExpectedStatus {
			w.retryAt[key] = now.Add(qualificationProbeRetry)
			w.log("candidate outbound probe did not pass", probe, "status", status)
			continue
		}
		if err := w.Store.RecordEnvironmentQualificationOutboundReceipt(ctx, probe, status); err != nil {
			// A changed binding or an ended lease refuses the receipt; the
			// next listing carries the current revision, if any.
			w.log("candidate outbound probe receipt was not recorded", probe, "err", err)
		}
	}
	return nil
}

func (w *QualificationProbeWorker) now() time.Time {
	if w.Now != nil {
		return w.Now()
	}
	return time.Now()
}

func (w *QualificationProbeWorker) log(msg string, probe api.OutboundQualificationProbe, args ...any) {
	if w.Log == nil {
		return
	}
	w.Log.Info(msg, append([]any{"request_id", probe.RequestID, "attempt", probe.Attempt, "integration_id", probe.IntegrationID}, args...)...)
}

// qualificationProbe sends the probe as the candidate's app and returns the
// response status, or 0 when the handler aborted. The body is discarded.
func (h *Handler) qualificationProbe(parent context.Context, probe api.OutboundQualificationProbe) int {
	principal := qualificationPrincipal{accountID: probe.AccountID, appID: probe.AppID, requestID: probe.RequestID,
		method: probe.Policy.Method, path: probe.Policy.Path}
	base := oteltrace.ContextWithSpan(context.Background(), oteltrace.SpanFromContext(parent))
	ctx, cancel := context.WithTimeout(context.WithValue(base, qualificationPrincipalKey{}, principal), qualificationProbeTimeout)
	stop := context.AfterFunc(parent, cancel)
	defer func() { stop(); cancel() }()
	request := routeAttemptRequest(&http.Request{Method: probe.Policy.Method, Header: http.Header{"Cache-Control": {"no-cache, no-store"}}},
		probe.IntegrationID, probe.Policy.Path, "", nil, true).WithContext(ctx) //nolint:contextcheck // probes carry no request values; ctx carries cancellation.
	recorder := &replayRecorder{header: make(http.Header), now: time.Now}
	serveReplay(h, recorder, request, nil)
	if recorder.aborted {
		return 0
	}
	return recorder.status
}
