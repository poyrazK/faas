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
