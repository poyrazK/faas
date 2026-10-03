package state

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// AppWebhookEventOutboxStore relays committed producer events into the delivery
// ledger. It is optional so Store test doubles do not need outbox methods.
type AppWebhookEventOutboxStore interface {
	DrainAppWebhookEventOutbox(context.Context, int) (int, error)
	RelayAppWebhookEventOutboxSource(context.Context, AppWebhookEvent, string) (bool, error)
}

// AppWebhookEventOutboxHealthStore reports the fleet backlog without exposing
// account, app, or webhook identifiers.
type AppWebhookEventOutboxHealthStore interface {
	AppWebhookEventOutboxHealth(context.Context) (AppWebhookEventOutboxHealth, error)
}

type AppWebhookEventOutboxHealth struct {
	PendingCount    int64
	OldestPendingAt *time.Time
}

// AppParkTransitionStore durably tracks a customer park request until the app
// has drained. The dispatcher calls DrainDrainedAppParkTransitions as a
// recovery path when the API process exits before observing the drain.
type AppParkTransitionStore interface {
	BeginAppParkTransition(context.Context, string, AppStatus) (AppParkTransition, bool, error)
	CompleteDrainedAppParkTransition(context.Context, string) (bool, error)
	DrainDrainedAppParkTransitions(context.Context, int) (int, error)
}

type AppParkTransition struct {
	ID    string
	AppID string
}

// AppWakeTransitionStore durably tracks a parked-to-active wake until the
// first ready instance can produce app.woken. Schedd recovery completes a
// transition if the request path exits after readiness.
type AppWakeTransitionStore interface {
	BeginAppWakeTransition(context.Context, string) (AppWakeTransition, bool, error)
	AbortAppWakeTransition(context.Context, string) (bool, error)
	CompleteReadyAppWakeTransition(context.Context, string, string, string) (bool, error)
	DrainReadyAppWakeTransitions(context.Context, int) (int, error)
}

// AppLifecycleTransitionHealthStore reports pending wake and park transitions
// without exposing app or account identifiers. It is used by fleet health
// metrics to detect lifecycle work that has stopped progressing.
type AppLifecycleTransitionHealthStore interface {
	AppLifecycleTransitionHealth(context.Context) (AppLifecycleTransitionHealth, error)
}

type AppLifecycleTransitionHealth struct {
	ParkPendingCount    int64
	ParkOldestPendingAt *time.Time
	WakePendingCount    int64
	WakeOldestPendingAt *time.Time
}

type AppWakeTransition struct {
	ID    string
	AppID string
}

type appWebhookOutboxEvent struct {
	ID                  string
	AccountID           string
	AppID               string
	Event               AppWebhookEvent
	SourceID            string
	Payload             json.RawMessage
	RecipientWebhookIDs []string
	CreatedAt           time.Time
}

func usageStatementFinalizedWebhookPayload(statement APIConsumerUsageStatement) (json.RawMessage, error) {
	if statement.FinalizedAt == nil {
		return nil, errors.New("state: finalized usage statement has no finalized_at")
	}
	payload := api.APIConsumerUsageStatementFinalizedWebhookPayload{
		AppID: statement.AppID, ConsumerID: statement.ConsumerID, StatementID: statement.ID,
		PeriodStart: statement.PeriodStart, PeriodEnd: statement.PeriodEnd,
		Currency: statement.Currency, BillableUnits: statement.BillableUnits,
		UnpricedUnits: statement.UnpricedUnits, AmountMillicents: statement.AmountMillicents,
		Priced: statement.Priced, AsOf: statement.AsOf.UTC().Format(time.RFC3339Nano),
		FinalizedAt: statement.FinalizedAt.UTC(),
		Buckets:     make([]api.APIConsumerUsageStatementBucketResponse, 0, len(statement.Buckets)),
	}
	for _, bucket := range statement.Buckets {
		payload.Buckets = append(payload.Buckets, api.APIConsumerUsageStatementBucketResponse{
			WindowStart: bucket.WindowStart.UTC(), BillableUnits: bucket.BillableUnits,
			RateCardID: bucket.RateCardID, Currency: bucket.Currency,
			PriceMillicentsPerUnit: bucket.PriceMillicentsPerUnit, AmountMillicents: bucket.AmountMillicents,
		})
	}
	return json.Marshal(payload)
}

func appParkedWebhookPayload(app App, occurredAt time.Time) (json.RawMessage, error) {
	return json.Marshal(map[string]any{
		"app_id": app.ID, "slug": app.Slug,
		"status": AppEvictedCold, "occurred_at": occurredAt.UTC(),
	})
}

func appWokenWebhookPayload(app App, instanceID, wakeID string, occurredAt time.Time) (json.RawMessage, error) {
	return json.Marshal(map[string]any{
		"app_id": app.ID, "slug": app.Slug, "status": AppActive,
		"instance_id": instanceID, "wake_id": wakeID, "occurred_at": occurredAt.UTC(),
	})
}
