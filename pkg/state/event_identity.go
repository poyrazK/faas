package state

import (
	"encoding/json"
	"strconv"

	"github.com/google/uuid"
)

// PublishedEventInvocationID identifies the original delivery independently of
// routing claims, retries, and replay generations. Keep its encoding stable.
func PublishedEventInvocationID(accountID, source, eventID, subscriptionID string) string {
	identity, _ := json.Marshal([4]string{accountID, source, eventID, subscriptionID})
	return uuid.NewSHA1(uuid.NameSpaceURL, identity).String()
}

// PublishedEventReacceptanceID is the delivery of a later acceptance of an
// event identity whose earlier receipt was pruned while that receipt's
// delivery is still retained (ADR-20261026010000). Salting with the later
// receipt keeps the two deliveries apart; the receipt records the identity it
// used in its recipient progress (PublishedEventRecipientProgress.DeliveryID).
func PublishedEventReacceptanceID(accountID, source, eventID, subscriptionID string, outboxID int64) string {
	identity, _ := json.Marshal([5]string{accountID, source, eventID, subscriptionID, strconv.FormatInt(outboxID, 10)})
	return uuid.NewSHA1(uuid.NameSpaceURL, identity).String()
}

// PublishedEventDeliveryID is the delivery a receipt's recipient was routed
// to: the identity its progress recorded, else the original identity.
func PublishedEventDeliveryID(progress PublishedEventRecipientProgress, accountID, source, eventID, subscriptionID string) string {
	if progress.DeliveryID != "" {
		return progress.DeliveryID
	}
	return PublishedEventInvocationID(accountID, source, eventID, subscriptionID)
}
