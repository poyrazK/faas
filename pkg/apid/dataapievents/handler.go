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
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/appevents"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/workloadidentity"
)

// Publisher accepts app producer-key events; *appevents.Service implements it.
type Publisher interface {
	PublishAppProducerEvent(ctx context.Context, acct state.Account, appID string, event appevents.AppProducerEvent) api.PublishEventBatchResult
	NotifyEventPublished(ctx context.Context)
}

// Store resolves the workload a token names.
type Store interface {
	state.AppOwnerReader
	AppByID(ctx context.Context, id string) (state.App, error)
	InstanceByID(ctx context.Context, id string) (state.Instance, error)
}

// Handler publishes a runtime's change events. Only a gregale:data-api:events
// token from a live instance of an active app is accepted; events publish
// under that app's own source, so the route grants nothing beyond it.
type Handler struct {
	Verifier  *workloadidentity.Verifier
	Store     Store
	Publisher Publisher
	Now       func() time.Time
}

func (h Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	acct, appID, err := h.workload(r)
	if err != nil {
		api.WriteProblem(w, api.NewProblem(http.StatusUnauthorized, api.CodeUnauthorized, "Workload identity required",
			"a gregale:data-api:events workload token from a live instance of an active app is required"))
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
	source, err := appevents.AppProducerSource(appID)
	if err != nil {
		api.WriteProblem(w, api.ErrInternal("application identity"))
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), api.EventPublishBatchTimeout)
	defer cancel()
	results := h.publish(ctx, acct, appID, batch.Events)
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(api.DataAPIChangeEventBatchResponse{Source: source, Results: results})
}

// workload verifies the token and resolves its live app and account.
func (h Handler) workload(r *http.Request) (state.Account, string, error) {
	if h.Verifier == nil || h.Store == nil {
		return state.Account{}, "", errors.New("workload trust is not configured")
	}
	token, _ := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	claims, err := h.Verifier.Verify(token, workloadidentity.DataAPIEventsAudience, h.now())
	if err != nil {
		return state.Account{}, "", err
	}
	app, err := h.Store.AppByID(r.Context(), claims.AppID)
	if err != nil || app.AccountID != claims.AccountID || app.Status == state.AppDeleted {
		return state.Account{}, "", errors.New("token does not name this app")
	}
	acct, err := state.AppResourceAccount(r.Context(), h.Store, app)
	if err != nil || !acct.Active() {
		return state.Account{}, "", errors.New("account is not active")
	}
	instance, err := h.Store.InstanceByID(r.Context(), claims.InstanceID)
	if err != nil || instance.AppID != app.ID || !state.State(instance.State).CountsForConcurrency() {
		return state.Account{}, "", errors.New("instance is not live")
	}
	return acct, app.ID, nil
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
