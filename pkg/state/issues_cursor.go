package state

import (
	"encoding/base64"
	"encoding/json"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

func EncodeIssueCursor(c IssueCursor) string {
	b, _ := json.Marshal(c)
	return base64.RawURLEncoding.EncodeToString(b)
}
func DecodeIssueCursor(value string) (IssueCursor, error) {
	if value == "" {
		return IssueCursor{}, nil
	}
	var c IssueCursor
	if len(value) > api.IssueCursorMaxBytes {
		return c, ErrInvalidArgument
	}
	b, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil {
		return c, ErrInvalidArgument
	}
	if err = json.Unmarshal(b, &c); err != nil || c.Time.IsZero() || c.Time.After(time.Now().Add(api.IssueMaxClockSkew)) {
		return c, ErrInvalidArgument
	}
	if _, err = uuid.Parse(c.ID); err != nil {
		return c, ErrInvalidArgument
	}
	return c, nil
}
