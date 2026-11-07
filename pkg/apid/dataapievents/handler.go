// Package dataapievents serves Data API change hooks (ADR-20261022121642):
// the private runtime route that publishes recorded row changes as
// app-scoped producer-key events (ADR-929), authenticated by workload identity.
package dataapievents

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/appevents"
	"github.com/onebox-faas/faas/pkg/apid/dataapiworkload"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/workloadidentity"
)

// Publisher accepts app producer-key events; *appevents.Service implements it.
type Publisher interface {
	PublishAppProducerEvent(ctx context.Context, acct state.Account, appID string, event appevents.AppProducerEvent) api.PublishEventBatchResult
	NotifyEventPublished(ctx context.Context)
}

// Store resolves the workload a token names.
type Store = dataapiworkload.Store

// Handler publishes a runtime's change events. Only a gregale:data-api:events
// token from a live instance of an active app is accepted; events publish
// under that app's own source, so the route grants nothing beyond it.
type Handler struct {
	Verifier  *workloadidentity.Verifier
	Store     Store
	Publisher Publisher
	// Admit decides a publication before any of its events is published:
	// nil, or the refusal every other publication path gives (events
	// paused, the account's publish rate). The relay retries a refused
	// publication after its back-off. Nil admits every publication.
	Admit func(ctx context.Context, acct state.Account, app state.App, events int) *api.Problem
	Now   func() time.Time
}

func (h Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	acct, app, err := h.workload(r)
	if err != nil {
		dataapiworkload.Refuse(w, err, "a gregale:data-api:events workload token from a live instance of an active app is required")
		return
	}
	if h.Publisher == nil {
		api.WriteProblem(w, api.ErrCapacity("event publication is unavailable"))
		return
	}
	batch, problem := decodeBatch(w, r)
	if problem != nil {
		api.WriteProblem(w, problem)
		return
	}
	source, err := appevents.AppProducerSource(app.ID)
	if err != nil {
		api.WriteProblem(w, api.ErrInternal("application identity"))
		return
	}
	if h.Admit != nil {
		if problem := h.Admit(r.Context(), acct, app, len(batch.Events)); problem != nil {
			api.WriteProblem(w, problem)
			return
		}
	}
	ctx, cancel := context.WithTimeout(r.Context(), api.EventPublishBatchTimeout)
	defer cancel()
	results := h.publish(ctx, acct, app.ID, batch.Events)
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(api.DataAPIChangeEventBatchResponse{Source: source, Results: results})
}

// workload verifies the token and resolves its live app and account. Events
// carry no deployment scope: a stage's changes would publish under the
// production app's source, and a cloned environment copies the setting and
// the change table, so only production publishes.
func (h Handler) workload(r *http.Request) (state.Account, state.App, error) {
	return dataapiworkload.Live(r, h.Verifier, workloadidentity.DataAPIEventsAudience, h.Store, h.now())
}

// publish publishes events in order. It stops at the first event that may
// succeed later, so the runtime retries it before anything recorded after it.
func (h Handler) publish(ctx context.Context, acct state.Account, appID string, events []api.DataAPIChangeEvent) []api.PublishEventBatchResult {
	results := make([]api.PublishEventBatchResult, len(events))
	stopped, accepted := false, false
	for i, event := range events {
		invalid := event.Validate()
		switch {
		case stopped:
			results[i] = appevents.AppProducerNotAttempted()
		case invalid != nil:
			results[i] = api.PublishEventBatchResult{Status: "rejected", Problem: api.ErrValidation(invalid.Error())}
		default:
			results[i] = h.Publisher.PublishAppProducerEvent(ctx, acct, appID, appevents.AppProducerEvent{
				Key: api.DataAPIChangeEventKeyPrefix + event.ID, Type: event.Type, Time: event.Time, Data: event.Data})
			accepted = accepted || results[i].Status == "accepted"
			stopped = results[i].Retryable
		}
		results[i].Index = i
	}
	if accepted {
		h.Publisher.NotifyEventPublished(ctx)
	}
	return results
}

func decodeBatch(w http.ResponseWriter, r *http.Request) (api.DataAPIChangeEventBatch, *api.Problem) {
	var batch api.DataAPIChangeEventBatch
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, api.DataAPIChangeEventsBatchMaxBytes))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&batch); err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			return batch, api.NewProblem(http.StatusRequestEntityTooLarge, "data_api_change_events_too_large", "Publication too large", "the publication exceeds its byte limit").
				WithLimit(api.DataAPIChangeEventsBatchMaxBytes, api.DataAPIChangeEventsBatchMaxBytes+1)
		}
		return batch, api.ErrValidation("the body must be one version 1 change event publication")
	}
	if !errors.Is(decoder.Decode(new(any)), io.EOF) || batch.Version != 1 {
		return batch, api.ErrValidation("the body must be one version 1 change event publication")
	}
	if len(batch.Events) == 0 || len(batch.Events) > api.DataAPIChangeEventsBatchMaxEvents {
		return batch, api.ErrValidation("a publication holds 1–100 events").WithLimit(api.DataAPIChangeEventsBatchMaxEvents, int64(len(batch.Events)))
	}
	return batch, nil
}

func (h Handler) now() time.Time {
	if h.Now != nil {
		return h.Now()
	}
	return time.Now()
}
