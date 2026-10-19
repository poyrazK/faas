// Package alertdelivery adapts the shared signed/guarded webhook transport to
// pipeline alerts. Kept outside the catalog package to avoid daemon import cycles.
package alertdelivery

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/datapipeline/alertwire"
	"github.com/onebox-faas/faas/pkg/oci"
	"github.com/onebox-faas/faas/pkg/webhookout"
)

// New returns one-attempt delivery; the catalog worker owns durable retries.
// A nil client always uses guarded egress. Client injection is for local tests.
func New(client *http.Client, log *slog.Logger) func(context.Context, alertwire.Attempt) alertwire.Result {
	return func(ctx context.Context, a alertwire.Attempt) alertwire.Result {
		attemptClient := client
		if attemptClient == nil {
			attemptClient = oci.NewEgressHTTPClient()
			defer attemptClient.CloseIdleConnections()
		}
		copyClient := *attemptClient
		copyClient.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
		ctx, cancel := context.WithTimeout(ctx, api.DataPipelineAlertAttemptTimeout)
		defer cancel()
		dispatch := webhookout.NewDispatcher(webhookout.DispatcherOptions{HTTPClient: &copyClient, MaxAttempts: 1, PerAttempt: api.DataPipelineAlertAttemptTimeout, FirstAttempt: a.Number, HeaderSet: webhookout.HeaderSetWebhook, Now: func() time.Time { return a.Now }, Logger: log})
		kind := "data_pipeline.health." + a.Kind
		event := webhookout.Event{ID: a.ID, OccurredAt: a.OccurredAt, Rule: kind, RuleName: kind, Payload: map[string]any{"version": 1, "pipeline_id": a.PipelineID, "incident_id": a.IncidentID, "kind": a.Kind, "code": a.Code}}
		result := dispatch.Dispatch(ctx, webhookout.Target{URL: a.URL, Signer: webhookout.NewSigner([]byte(a.Secret))}, event)
		return alertwire.Result{StatusCode: result.StatusCode, RetryAfter: result.RetryAfter, Accepted: result.Err == nil && result.StatusCode >= 200 && result.StatusCode < 300, Redirect: result.StatusCode >= 300 && result.StatusCode < 400, Terminal: errors.Is(result.Err, webhookout.ErrTerminal) || errors.Is(result.Err, oci.ErrImageEgressDenied)}
	}
}
