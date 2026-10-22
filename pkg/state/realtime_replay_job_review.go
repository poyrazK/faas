package state

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
	q "github.com/onebox-faas/faas/pkg/state/realtimebulk/sqlc"
)

// The caller holds the group and report locks for the entire promotion transaction.
func (s *PgStore) startReviewedRealtimeReplay(ctx context.Context, tx pgx.Tx, report q.ManagedRealtimeReplayJob) (RealtimeReplayJob, error) {
	if !report.DryRun || report.State != "completed" {
		return RealtimeReplayJob{}, ErrConflict
	}
	queries := q.New()
	if report.StartedJobID.Valid {
		row, err := queries.ReadReplayJob(ctx, tx, q.ReadReplayJobParams{GroupID: report.GroupID, ID: report.StartedJobID})
		if errors.Is(err, pgx.ErrNoRows) {
			return RealtimeReplayJob{}, ErrConflict
		}
		if err != nil {
			return RealtimeReplayJob{}, err
		}
		result, err := replayJobView(row)
		if err != nil {
			return result, err
		}
		return result, tx.Commit(ctx)
	}
	var selections []RealtimeDeadLetterReplaySelection
	var outcomes []RealtimeReplayJobOutcome
	if err := json.Unmarshal(report.Selection, &selections); err != nil {
		return RealtimeReplayJob{}, err
	}
	if err := json.Unmarshal(report.Outcomes, &outcomes); err != nil {
		return RealtimeReplayJob{}, err
	}
	if int(report.Position) != len(selections) || len(outcomes) != len(selections) {
		return RealtimeReplayJob{}, ErrConflict
	}
	items := []RealtimeDeadLetterReplaySelection{}
	for i, outcome := range outcomes {
		if outcome.Sequence != selections[i].Sequence {
			return RealtimeReplayJob{}, ErrConflict
		}
		if outcome.Status == "eligible" {
			items = append(items, selections[i])
		}
	}
	if len(items) == 0 {
		return RealtimeReplayJob{}, ErrConflict
	}
	count, err := queries.CountActiveReplayJobs(ctx, tx, report.GroupID)
	if err != nil {
		return RealtimeReplayJob{}, err
	}
	if count >= api.RealtimeReplayJobMaxActive {
		return RealtimeReplayJob{}, ErrConflict
	}
	// Keep the reviewed report through this prune even if newer terminal jobs exist.
	if err = queries.PruneReplayJobsExcept(ctx, tx, q.PruneReplayJobsExceptParams{GroupID: report.GroupID, KeepID: report.ID, RetainCount: api.RealtimeReplayJobRetainedTerminal - 1}); err != nil {
		return RealtimeReplayJob{}, err
	}
	encoded, err := json.Marshal(items)
	if err != nil {
		return RealtimeReplayJob{}, err
	}
	row, err := queries.CreateReplayJob(ctx, tx, q.CreateReplayJobParams{ID: replayJobUUID(uuid.NewString()), GroupID: report.GroupID, Rate: report.Rate, Selection: encoded, ReviewedJobID: report.ID})
	if err != nil {
		return RealtimeReplayJob{}, err
	}
	if err = queries.MarkReviewedReplayStarted(ctx, tx, q.MarkReviewedReplayStartedParams{GroupID: report.GroupID, ID: report.ID, StartedJobID: row.ID}); err != nil {
		return RealtimeReplayJob{}, err
	}
	result, err := replayJobView(row)
	if err != nil {
		return result, err
	}
	return result, tx.Commit(ctx)
}
