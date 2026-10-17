package state

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/hosting/activity"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

var _ HostingActivityReader = (*PgStore)(nil)

func (s *PgStore) ListHostingActivityRecords(ctx context.Context, account, site, before string) (HostingActivityRecords, error) {
	at, id, err := activity.ParseCursor(before)
	if err != nil {
		return HostingActivityRecords{}, ErrInvalidArgument
	}
	if _, err = s.GetHostingSite(ctx, account, site); err != nil {
		return HostingActivityRecords{}, err
	}
	if before == "" {
		at = time.Unix(0, 0)
		id = uuid.Nil.String()
	}
	a, _ := parsePgUUID(account)
	si, _ := parsePgUUID(site)
	cursorID, _ := parsePgUUID(id)
	rows, err := sqlc.New().HostingActivityList(ctx, s.pool, sqlc.HostingActivityListParams{AccountID: a, SiteID: si, HasCursor: before != "", BeforeCreatedAt: pgtype.Timestamptz{Time: at, Valid: true}, BeforeID: cursorID, PageSize: api.HostingActivityPageSize + 1})
	if err != nil {
		return HostingActivityRecords{}, fmt.Errorf("list hosting activity: %w", err)
	}
	out := make([]HostingActivityRecord, 0, len(rows))
	for _, r := range rows {
		row := HostingActivityRecord{OtherPreviews: r.OtherPreviews, Run: api.HostingCIRun{ID: uuidString(r.ID), SiteID: uuidString(r.SiteID), ReleaseID: uuidString(r.ReleaseID), PreviewID: uuidString(r.PreviewID), Canceled: r.Canceled, PreviewCreated: r.PreviewCreated, CreatedAt: r.CreatedAt.Time, PreviewExpiresAt: r.PreviewExpiresAt.Time}}
		if r.ReleaseRecordID.Valid {
			row.Release = &api.HostingRelease{ID: uuidString(r.ReleaseRecordID), SiteID: site, State: r.ReleaseState.String, FirstPromotedAt: hostingTimePointer(r.ReleaseFirstPromotedAt)}
			if len(r.ReleaseProvenance) > 0 {
				if err = json.Unmarshal(r.ReleaseProvenance, &row.Release.Provenance); err != nil {
					return HostingActivityRecords{}, fmt.Errorf("decode hosting activity source: %w", err)
				}
			}
		}
		if r.PreviewRecordID.Valid {
			row.Preview = &api.HostingPreview{ID: uuidString(r.PreviewRecordID), AccountID: account, SiteID: site, ReleaseID: uuidString(r.PreviewReleaseID), OriginID: r.PreviewOriginID.String, ExpiresAt: r.PreviewExpiry.Time, Revoked: r.PreviewRevoked.Bool, Generation: r.PreviewGeneration.Int64, PublishedGeneration: r.PreviewPublishedGeneration.Int64, PublishedAt: hostingTimePointer(r.PreviewPublishedAt), NextAttemptAt: r.PreviewNextAttemptAt.Time, LastErrorCode: r.PreviewLastErrorCode.String}
		}
		out = append(out, row)
	}
	return hostingActivityPage(out), nil
}
