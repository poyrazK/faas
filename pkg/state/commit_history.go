package state

import (
	"context"
	"encoding/base64"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// CommitPublicationHistoryStore is a read-only capability separate from admission.
type CommitPublicationHistoryStore interface {
	ListCommitPublications(context.Context, string, string, string, int) (api.CommitPublicationHistoryResponse, error)
}

func commitHistoryCursor(source string, at time.Time, id string) string {
	return base64.RawURLEncoding.EncodeToString([]byte(source + "\n" + at.UTC().Format(time.RFC3339Nano) + "\n" + id))
}

func parseCommitHistoryCursor(source, cursor string) (pgtype.Timestamptz, string, error) {
	if cursor == "" {
		return pgtype.Timestamptz{InfinityModifier: pgtype.Infinity, Valid: true}, "ffffffff-ffff-ffff-ffff-ffffffffffff", nil
	}
	if len(cursor) > api.CommitPublicationHistoryCursorMaxBytes {
		return pgtype.Timestamptz{}, "", ErrInvalidArgument
	}
	raw, err := base64.RawURLEncoding.DecodeString(cursor)
	parts := strings.Split(string(raw), "\n")
	if err != nil || len(parts) != 3 || parts[0] != source {
		return pgtype.Timestamptz{}, "", ErrInvalidArgument
	}
	at, err := time.Parse(time.RFC3339Nano, parts[1])
	id, idErr := uuid.Parse(parts[2])
	if err != nil || idErr != nil || id == uuid.Nil || commitHistoryCursor(source, at, id.String()) != cursor {
		return pgtype.Timestamptz{}, "", ErrInvalidArgument
	}
	return pgtype.Timestamptz{Time: at, Valid: true}, id.String(), nil
}

func (s *PgStore) ListCommitPublications(ctx context.Context, account, source, before string, limit int) (api.CommitPublicationHistoryResponse, error) {
	out := api.CommitPublicationHistoryResponse{Items: []api.CommitPublicationHistoryItem{}}
	sourceID, err := uuid.Parse(source)
	if err != nil || sourceID == uuid.Nil || limit < 0 || limit > api.CommitPublicationHistoryPageMax {
		return out, ErrInvalidArgument
	}
	source = sourceID.String()
	at, id, err := parseCommitHistoryCursor(source, before)
	if err != nil {
		return out, err
	}
	src, err := s.CommitSourceByID(ctx, account, source)
	if err != nil {
		return out, err
	}
	if src.Destination != "event" {
		return out, ErrInvalidArgument
	}
	if limit == 0 {
		limit = api.CommitPublicationHistoryPageDefault
	}
	rows, err := sqlc.New().ListCommitPublications(ctx, s.pool, sqlc.ListCommitPublicationsParams{AccountID: account, SourceID: source, BeforeAt: at, BeforeID: id, PageLimit: int32(limit + 1)})
	if err != nil {
		return out, mapErr(err)
	}
	more := len(rows) > limit
	if more {
		rows = rows[:limit]
	}
	for _, row := range rows {
		out.Items = append(out.Items, api.CommitPublicationHistoryItem{CommitReceiptResponse: api.CommitReceiptResponse{ID: row.ID, SourceID: row.SourceID, EventID: row.EventID, AcceptedAt: row.AcceptedAt.Time, Publication: commitPublicationIdentity(row.PublicationAcceptanceID.Int64, row.PublicationSource.String, row.PublicationEventID.String, row.AcceptedAt.Time)}, Type: row.EventType, SchemaVersion: row.SchemaVersion})
	}
	if more {
		last := rows[len(rows)-1]
		out.NextBefore = commitHistoryCursor(source, last.AcceptedAt.Time, last.ID)
	}
	return out, nil
}
