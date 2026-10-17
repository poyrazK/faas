// Package activity implements bounded pagination and stored-evidence summaries.
package activity

import (
	"encoding/base64"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

func Cursor(at time.Time, id string) string {
	return base64.RawURLEncoding.EncodeToString([]byte(at.UTC().Format(time.RFC3339Nano) + "|" + id))
}

// ParseCursor accepts an empty first-page cursor or one canonical UTC time/UUID
// tuple. It grants no authority; each page is independently scoped to its site.
func ParseCursor(raw string) (time.Time, string, error) {
	if raw == "" {
		return time.Time{}, "", nil
	}
	invalid := errors.New("invalid deployment activity cursor")
	if len(raw) > api.HostingActivityCursorMaxBytes {
		return time.Time{}, "", invalid
	}
	decoded, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil {
		return time.Time{}, "", invalid
	}
	timestamp, id, ok := strings.Cut(string(decoded), "|")
	at, err := time.Parse(time.RFC3339Nano, timestamp)
	parsed, uuidErr := uuid.Parse(id)
	if !ok || err != nil || at.IsZero() || at.Before(time.Unix(0, 0)) || uuidErr != nil || parsed == uuid.Nil || parsed.String() != id || Cursor(at, id) != raw {
		return time.Time{}, "", invalid
	}
	return at, id, nil
}
