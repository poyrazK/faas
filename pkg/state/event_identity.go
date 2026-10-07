package state

import (
	"encoding/json"

	"github.com/google/uuid"
)

// PublishedEventInvocationID identifies the original delivery independently of
// routing claims, retries, and replay generations. Keep its encoding stable.
func PublishedEventInvocationID(accountID, source, eventID, subscriptionID string) string {
	identity, _ := json.Marshal([4]string{accountID, source, eventID, subscriptionID})
	return uuid.NewSHA1(uuid.NameSpaceURL, identity).String()
}
