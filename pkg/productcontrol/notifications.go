package productcontrol

import (
	"encoding/hex"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

func NotificationReference(value string) (kind, id string, ok bool) {
	kind, id, found := strings.Cut(value, "/")
	u, err := uuid.Parse(id)
	return kind, id, found && (kind == "help" || kind == "work") && err == nil && u != uuid.Nil && u.String() == id
}

func ValidateNotificationUpdate(r api.ProductNotificationUpdate) error {
	_, _, ok := NotificationReference(r.ID)
	digest, err := hex.DecodeString(r.Revision)
	if !ok || err != nil || len(digest) != 32 || hex.EncodeToString(digest) != r.Revision || r.ExpectedVersion == nil || *r.ExpectedVersion < 0 || *r.ExpectedVersion > api.ProductControlNotificationMaxUpdates || r.Read == nil {
		return fmt.Errorf("notification requires its current reference/revision, explicit read state and read version")
	}
	return nil
}
