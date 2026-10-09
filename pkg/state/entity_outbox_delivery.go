// adr: 843
package state

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

// EntityOutboxDeliveryStore durably accepts one already committed entity intent.
// The stable delivery ID and fingerprint receipt survive delivery retention.
// A repeat never resets a delivery's retry, success or dead-letter state.
type EntityOutboxDeliveryStore interface {
	AcceptEntityOutboxDelivery(context.Context, AppWebhookDelivery) (string, error)
}

var ErrEntityOutboxInvalid = errors.New("state: invalid entity outbox acceptance")

func entityOutboxFingerprint(in AppWebhookDelivery) (string, error) {
	for _, id := range []string{in.ID, in.AccountID, in.AppID, in.WebhookID} {
		parsed, err := uuid.Parse(id)
		if err != nil || parsed == uuid.Nil || parsed.String() != id {
			return "", ErrEntityOutboxInvalid
		}
	}
	event := string(in.Event)
	if strings.TrimSpace(event) == "" || len(event) > api.MaxDurableEntityIdentityBytes || !utf8.ValidString(event) || strings.IndexFunc(event, func(r rune) bool { return r < 32 || r == 127 }) >= 0 ||
		!json.Valid(in.Payload) || len(in.Payload) > api.MaxDurableEntityOutboxDeliveryBytes {
		return "", ErrEntityOutboxInvalid
	}
	body, err := json.Marshal(struct {
		Account, App, Webhook, Event string
		Payload                      json.RawMessage
	}{in.AccountID, in.AppID, in.WebhookID, event, in.Payload})
	if err != nil {
		return "", fmt.Errorf("encode entity outbox acceptance: %w", err)
	}
	hash := sha256.Sum256(body)
	return hex.EncodeToString(hash[:]), nil
}
