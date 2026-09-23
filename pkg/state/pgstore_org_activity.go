package state

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
)

const orgActivityColumns = `id, org_id, occurred_at, kind, actor_type,
       actor_account_id, actor_label, resource_type, resource_id,
       resource_label, app_id, project_id, deployment_id, data,
       source_type, source_id`

// AppendOrgActivity writes one curated customer-facing fact. The source key
// is idempotent: a repeated producer delivery returns the original row instead
// of creating a second timeline entry.
func (s *PgStore) AppendOrgActivity(ctx context.Context, entry OrgActivity) (OrgActivity, error) {
	entry, err := normalizeOrgActivity(entry, time.Now())
	if err != nil {
		return OrgActivity{}, err
	}
	_, err = s.pool.Exec(ctx, `
		insert into org_activity (
			org_id, occurred_at, kind, actor_type, actor_account_id,
			actor_label, resource_type, resource_id, resource_label,
			app_id, project_id, deployment_id, data, source_type, source_id
		) values (
			$1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12,
			$13::jsonb, $14, $15
		)
		on conflict (org_id, source_type, source_id) do nothing`,
		entry.OrgID, entry.OccurredAt, entry.Kind, string(entry.ActorType),
		entry.ActorAccountID, entry.ActorLabel, entry.ResourceType,
		nullString(entry.ResourceID), entry.ResourceLabel, entry.AppID,
		entry.ProjectID, entry.DeploymentID, []byte(entry.Data),
		entry.SourceType, entry.SourceID)
	if err != nil {
		return OrgActivity{}, fmt.Errorf("state: append org activity: %w", err)
	}
	row := s.pool.QueryRow(ctx, `select `+orgActivityColumns+`
		from org_activity
		where org_id = $1 and source_type = $2 and source_id = $3`,
		entry.OrgID, entry.SourceType, entry.SourceID)
	stored, err := scanOrgActivity(row)
	if err != nil {
		return OrgActivity{}, fmt.Errorf("state: read appended org activity: %w", err)
	}
	return stored, nil
}

// ListOrgActivity returns a tenant-pinned keyset page ordered newest first.
// The handler requests one extra row to decide whether next_before is needed.
func (s *PgStore) ListOrgActivity(ctx context.Context, filter OrgActivityFilter) ([]OrgActivity, error) {
	if filter.OrgID == uuid.Nil {
		return nil, fmt.Errorf("state: list org activity requires org id")
	}
	var beforeAt, beforeID any
	if filter.Before != nil {
		beforeAt = filter.Before.OccurredAt
		beforeID = filter.Before.ID
	}
	rows, err := s.pool.Query(ctx, `select `+orgActivityColumns+`
		from org_activity
		where org_id = $1
		  and ($2::timestamptz is null
		       or occurred_at < $2::timestamptz
		       or (occurred_at = $2::timestamptz and id < $3::bigint))
		  and ($4 = '' or left(kind, length($4)) = $4)
		  and ($5 = '' or actor_type = $5)
		  and ($6::uuid is null or app_id = $6::uuid)
		order by occurred_at desc, id desc
		limit $7`, filter.OrgID, beforeAt, beforeID, filter.KindPrefix,
		string(filter.ActorType), filter.AppID, normalizeOrgActivityLimit(filter.Limit))
	if err != nil {
		return nil, fmt.Errorf("state: list org activity: %w", err)
	}
	defer rows.Close()
	out := make([]OrgActivity, 0)
	for rows.Next() {
		entry, err := scanOrgActivity(rows)
		if err != nil {
			return nil, fmt.Errorf("state: scan org activity: %w", err)
		}
		out = append(out, entry)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("state: list org activity rows: %w", err)
	}
	return out, nil
}

func scanOrgActivity(row interface{ Scan(...any) error }) (OrgActivity, error) {
	var entry OrgActivity
	var resourceID *string
	var data []byte
	err := row.Scan(&entry.ID, &entry.OrgID, &entry.OccurredAt, &entry.Kind,
		&entry.ActorType, &entry.ActorAccountID, &entry.ActorLabel,
		&entry.ResourceType, &resourceID, &entry.ResourceLabel, &entry.AppID,
		&entry.ProjectID, &entry.DeploymentID, &data, &entry.SourceType,
		&entry.SourceID)
	if err != nil {
		return OrgActivity{}, err
	}
	if resourceID != nil {
		entry.ResourceID = *resourceID
	}
	entry.Data = json.RawMessage(data)
	return entry, nil
}
