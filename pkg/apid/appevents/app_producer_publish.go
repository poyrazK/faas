package appevents

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/state"
	pkgtrace "github.com/onebox-faas/faas/pkg/trace"
)

// AppProducerEvent is one application-scoped producer-key publication
// (ADR-929): the app's own source, a stable key, type, time and data.
type AppProducerEvent struct {
	Key  string
	Type string
	Time *time.Time
	Data json.RawMessage
}

// AppProducerSource is the event source of an app's producer keys.
func AppProducerSource(app string) (string, error) {
	id, err := uuid.Parse(app)
	if err != nil || id == uuid.Nil {
		return "", api.ErrValidation("invalid application identity")
	}
	return "app." + id.String(), nil
}

// AppProducerEventIdentity derives the canonical app ID, source and event ID
// of an app producer key.
func AppProducerEventIdentity(app, key string) (string, string, string, error) {
	source, err := AppProducerSource(app)
	if err != nil {
		return "", "", "", err
	}
	if err := api.ValidateAppEventProducerKey(key); err != nil {
		return "", "", "", err
	}
	digest := sha256.Sum256([]byte(key))
	return source[len("app."):], source, "key." + hex.EncodeToString(digest[:]), nil
}

// PublishAppProducerEvent accepts one app producer-key event and reports it
// as a batch result: accepted, duplicate, or rejected and whether retrying
// the same key and content may succeed. Matching retained publications are
// recognized before current schema rules, as for single publication.
func (s *Service) PublishAppProducerEvent(ctx context.Context, acct state.Account, appID string, event AppProducerEvent) api.PublishEventBatchResult {
	if ctx.Err() != nil {
		return AppProducerNotAttempted()
	}
	_, source, id, err := AppProducerEventIdentity(appID, event.Key)
	if err != nil {
		return batchPublishFailure("rejected", false, api.ErrValidation(err.Error()))
	}
	envelope, err := NormalizePublishRequest(api.PublishEventRequest{ID: id, Source: source, Type: event.Type, Time: event.Time, Data: event.Data}, acct.ID)
	if err != nil {
		return batchPublishFailure("rejected", false, api.ErrValidation(err.Error()))
	}
	trace := pkgtrace.InjectHeaders(ctx)
	envelope.Traceparent, envelope.Tracestate, envelope.Baggage = trace["traceparent"], trace["tracestate"], trace["baggage"]
	payload, err := json.Marshal(envelope)
	if err != nil {
		return batchPublishFailure("rejected", false, api.ErrValidation("invalid event data"))
	}
	lookup, okLookup := s.Host.Store().(state.PublishedEventLookupStore)
	store, okStore := s.Host.Store().(state.PublishedEventAcceptanceStore)
	if !okLookup || !okStore {
		return batchPublishFailure("rejected", true, api.ErrInternal("event acceptance store"))
	}
	accepted, err := lookup.LookupPublishedEventAcceptance(ctx, acct.ID, payload)
	if errors.Is(err, state.ErrNotFound) {
		if problem, retryable := s.batchEventSchemaProblem(ctx, acct.ID, api.PublishEventRequest{Source: source, Type: event.Type, Data: event.Data}); problem != nil {
			return batchPublishFailure("rejected", retryable, problem)
		}
		accepted, err = store.AcceptPublishedEvent(ctx, "apid", acct.ID, payload)
	}
	if err != nil {
		return batchEventAcceptanceFailure(err)
	}
	status := "accepted"
	if accepted.Duplicate {
		status = "duplicate"
	}
	return api.PublishEventBatchResult{Status: status, Receipt: &api.PublishEventResponse{ID: id, AccountID: acct.ID, AcceptedAt: accepted.AcceptedAt, ReceiptURL: EventReceiptURL(source, id)}}
}

// AppProducerNotAttempted is the result for an event a publication stopped
// before; retrying the same key and content may succeed.
func AppProducerNotAttempted() api.PublishEventBatchResult {
	return batchPublishFailure("rejected", true, api.NewProblem(http.StatusServiceUnavailable, "event_publish_not_attempted", "Event not attempted", "retry the same key and content"))
}

// NotifyEventPublished wakes event routing after new acceptances.
func (s *Service) NotifyEventPublished(ctx context.Context) {
	_ = s.Host.Notify(ctx, db.NotifyEventPublished, "1")
}
