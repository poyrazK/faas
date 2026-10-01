package devbridge

import (
	"github.com/google/uuid"
	"time"
)

// WebhookReplayID lets the CLI identify a receipt even if the reply is lost.
func WebhookReplayID(session, key string) string {
	return uuid.NewSHA1(uuid.NameSpaceURL, []byte("gregale:dev-bridge-replay:"+session+"\x00"+key)).String()
}

// WebhookReplay is a separate copy receipt, never a mutation of the original
// invocation. An uncertain or interrupted dispatch is not automatically retried.
type WebhookReplay struct {
	ID             string     `json:"id"`
	SessionID      string     `json:"session_id"`
	AccountID      string     `json:"-"`
	InvocationID   string     `json:"invocation_id"`
	IdempotencyKey string     `json:"-"`
	State          string     `json:"state"`
	HTTPStatus     int        `json:"http_status"`
	CreatedAt      time.Time  `json:"created_at"`
	CompletedAt    *time.Time `json:"completed_at,omitempty"`
}
