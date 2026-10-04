package faas

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"unicode/utf8"
)

// InsertCommitEvent inserts an outbox event using the caller's existing
// PostgreSQL business transaction. It never commits, performs DDL, or calls
// Gregale. Commit or roll back the business write and this insert together.
// Install the documented public.gregale_outbox schema first.
//
// An empty event ID generates a UUID. Keep the returned ID for correlation;
// retries of a logical business transaction should retain the original ID.
// A duplicate ID is an error, so a changed event cannot be silently dropped.
func InsertCommitEvent(ctx context.Context, tx *sql.Tx, event CommitEventRequest) (string, error) {
	if tx == nil {
		return "", errors.New("commit: an existing transaction is required")
	}
	identity := strings.ToLower(event.ID)
	if identity == "" {
		var data [16]byte
		if _, err := rand.Read(data[:]); err != nil {
			return "", err
		}
		data[6] = (data[6] & 0x0f) | 0x40
		data[8] = (data[8] & 0x3f) | 0x80
		encoded := hex.EncodeToString(data[:])
		identity = encoded[:8] + "-" + encoded[8:12] + "-" + encoded[12:16] + "-" + encoded[16:20] + "-" + encoded[20:]
	}
	if !commitUUID(identity) {
		return "", errors.New("commit: event ID must be a UUID")
	}
	if !utf8.ValidString(event.Type) || utf8.RuneCountInString(event.Type) < 1 || utf8.RuneCountInString(event.Type) > 256 {
		return "", errors.New("commit: event type must contain 1-256 characters")
	}
	if !json.Valid(event.Data) {
		return "", errors.New("commit: event data must be valid JSON")
	}
	_, err := tx.ExecContext(ctx, "INSERT INTO public.gregale_outbox(event_id,event_type,payload) VALUES ($1::uuid,$2,$3::jsonb)", identity, event.Type, string(event.Data))
	if err != nil {
		return "", err
	}
	return identity, nil
}

func commitUUID(value string) bool {
	if len(value) != 36 {
		return false
	}
	for i, c := range value {
		if i == 8 || i == 13 || i == 18 || i == 23 {
			if c != '-' {
				return false
			}
			continue
		}
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}
