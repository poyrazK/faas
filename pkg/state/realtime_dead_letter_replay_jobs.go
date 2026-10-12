package state

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/api"
	q "github.com/onebox-faas/faas/pkg/state/realtimebulk/sqlc"
)

type RealtimeReplayJobRequest struct {
	DryRun          bool     `json:"-"`
	FailureCodes    []string `json:"failure_codes,omitempty"`
	AfterSequence   string   `json:"after_sequence,omitempty"`
	ThroughSequence string   `json:"through_sequence,omitempty"`
	Rate            int      `json:"rate"`
}
type RealtimeReplayJobOutcome struct {
	RealtimeDeadLetterReplayOutcome
	CheckedAt *time.Time `json:"checked_at,omitempty"`
}
type RealtimeReplayJob struct {
	DryRun        bool    `json:"dry_run"`
	Eligible      int     `json:"eligible"`
	ReviewedJobID *string `json:"reviewed_job_id"`
	StartedJobID  *string `json:"started_job_id"`

	ErrorCount int64                      `json:"error_count"`
	LastError  string                     `json:"last_error"`
	ID         string                     `json:"id"`
	GroupID    string                     `json:"group_id"`
	State      string                     `json:"state"`
	Rate       int                        `json:"rate"`
	Selected   int                        `json:"selected"`
	Processed  int                        `json:"processed"`
	Replayed   int                        `json:"replayed"`
	Skipped    int                        `json:"skipped"`
	Outcomes   []RealtimeReplayJobOutcome `json:"outcomes"`
	CreatedAt  time.Time                  `json:"created_at"`
	UpdatedAt  time.Time                  `json:"updated_at"`
}
type RealtimeReplayJobStore interface {
	CreateRealtimeReplayJob(context.Context, string, string, string, RealtimeReplayJobRequest) (RealtimeReplayJob, error)
	ListRealtimeReplayJobs(context.Context, string, string, string) ([]RealtimeReplayJob, error)
	ControlRealtimeReplayJob(context.Context, string, string, string, string, string) (RealtimeReplayJob, error)
	RunRealtimeReplayJobs(context.Context) error
}

func replayJobUUID(value string) pgtype.UUID { var id pgtype.UUID; _ = id.Scan(value); return id }
func replayJobView(row q.ManagedRealtimeReplayJob) (RealtimeReplayJob, error) {
	var items []RealtimeDeadLetterReplaySelection
	result := RealtimeReplayJob{DryRun: row.DryRun, ReviewedJobID: replayJobIDPtr(row.ReviewedJobID), StartedJobID: replayJobIDPtr(row.StartedJobID), ErrorCount: row.ErrorCount, LastError: row.LastError, ID: uuid.UUID(row.ID.Bytes).String(), GroupID: uuid.UUID(row.GroupID.Bytes).String(), State: row.State, Rate: int(row.Rate), Processed: int(row.Position), CreatedAt: row.CreatedAt.Time.UTC(), UpdatedAt: row.UpdatedAt.Time.UTC(), Outcomes: []RealtimeReplayJobOutcome{}}
	if err := json.Unmarshal(row.Selection, &items); err != nil {
		return result, err
	}
	if err := json.Unmarshal(row.Outcomes, &result.Outcomes); err != nil {
		return result, err
	}
	result.Selected = len(items)
	for _, outcome := range result.Outcomes {
		switch outcome.Status {
		case "replayed":
			result.Replayed++
		case "eligible":
			result.Eligible++
		default:
			result.Skipped++
		}
	}
	return result, nil
}
func accountReplayJob(ctx context.Context) error {
	if _, ok := realtimeWorkerFromContext(ctx); ok {
		return ErrRealtimeWorkerCredentialDenied
	}
	return nil
}
func (s *PgStore) CreateRealtimeReplayJob(ctx context.Context, ep, ch, name string, req RealtimeReplayJobRequest) (RealtimeReplayJob, error) {
	if err := accountReplayJob(ctx); err != nil {
		return RealtimeReplayJob{}, err
	}
	preview := RealtimeDeadLetterPreviewRequest{FailureCodes: req.FailureCodes, AfterSequence: req.AfterSequence, ThroughSequence: req.ThroughSequence}
	after, through, err := validateRealtimeDeadPreview(&preview)
	if err != nil || req.Rate < 1 || req.Rate > api.RealtimeReplayJobMaxRate {
		return RealtimeReplayJob{}, ErrRealtimeConsumerGroupInvalid
	}
	tx, g, err := s.lockRealtimeGroup(ctx, ep, ch, name)
	if err != nil {
		return RealtimeReplayJob{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	queries := q.New()
	gid := replayJobUUID(g.id)
	count, err := queries.CountActiveReplayJobs(ctx, tx, gid)
	if err != nil {
		return RealtimeReplayJob{}, err
	}
	if count >= api.RealtimeReplayJobMaxActive {
		return RealtimeReplayJob{}, ErrConflict
	}
	codes := req.FailureCodes
	if codes == nil {
		codes = []string{}
	}
	rows, err := queries.PreviewDeadLetters(ctx, tx, q.PreviewDeadLettersParams{GroupID: g.id, AfterSequence: after, ThroughSequence: through, FailureCodes: codes, RowLimit: api.RealtimeReplayJobMaxSelection + 1, RetentionSeconds: int32(g.retentionSeconds), Ordered: g.orderingField != ""})
	if err != nil {
		return RealtimeReplayJob{}, err
	}
	if len(rows) == 0 || len(rows) > api.RealtimeReplayJobMaxSelection {
		return RealtimeReplayJob{}, ErrRealtimeConsumerGroupInvalid
	}
	items := make([]RealtimeDeadLetterReplaySelection, 0, len(rows))
	for _, row := range rows {
		items = append(items, RealtimeDeadLetterReplaySelection{Sequence: row.Sequence, DeadSince: row.DeadSince.Time.UTC()})
	}
	selection, err := json.Marshal(items)
	if err != nil {
		return RealtimeReplayJob{}, err
	}
	if err = queries.PruneReplayJobs(ctx, tx, q.PruneReplayJobsParams{GroupID: gid, RetainCount: api.RealtimeReplayJobRetainedTerminal}); err != nil {
		return RealtimeReplayJob{}, err
	}
	row, err := queries.CreateReplayJob(ctx, tx, q.CreateReplayJobParams{ID: replayJobUUID(uuid.NewString()), GroupID: gid, Rate: int32(req.Rate), Selection: selection, DryRun: req.DryRun})
	if err != nil {
		return RealtimeReplayJob{}, err
	}
	result, err := replayJobView(row)
	if err != nil {
		return result, err
	}
	return result, tx.Commit(ctx)
}
func (s *PgStore) ListRealtimeReplayJobs(ctx context.Context, ep, ch, name string) ([]RealtimeReplayJob, error) {
	if err := accountReplayJob(ctx); err != nil {
		return nil, err
	}
	tx, g, err := s.lockRealtimeGroup(ctx, ep, ch, name)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	rows, err := q.New().ListReplayJobs(ctx, tx, q.ListReplayJobsParams{GroupID: replayJobUUID(g.id), RowLimit: api.RealtimeReplayJobListMax})
	if err != nil {
		return nil, err
	}
	result := []RealtimeReplayJob{}
	for _, row := range rows {
		job, err := replayJobView(row)
		if err != nil {
			return nil, err
		}
		result = append(result, job)
	}
	return result, tx.Commit(ctx)
}
func (s *PgStore) ControlRealtimeReplayJob(ctx context.Context, ep, ch, name, id, action string) (RealtimeReplayJob, error) {
	if err := accountReplayJob(ctx); err != nil {
		return RealtimeReplayJob{}, err
	}
	parsed, err := uuid.Parse(id)
	if err != nil {
		return RealtimeReplayJob{}, ErrRealtimeConsumerGroupInvalid
	}
	id = parsed.String()
	if action != "pause" && action != "resume" && action != "cancel" && action != "start" {
		return RealtimeReplayJob{}, ErrRealtimeConsumerGroupInvalid
	}
	tx, g, err := s.lockRealtimeGroup(ctx, ep, ch, name)
	if err != nil {
		return RealtimeReplayJob{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	queries := q.New()
	row, err := queries.ReadReplayJob(ctx, tx, q.ReadReplayJobParams{GroupID: replayJobUUID(g.id), ID: replayJobUUID(id)})
	if errors.Is(err, pgx.ErrNoRows) {
		return RealtimeReplayJob{}, ErrNotFound
	}
	if err != nil {
		return RealtimeReplayJob{}, err
	}
	if action == "start" {
		return s.startReviewedRealtimeReplay(ctx, tx, row)
	}
	if row.State == "completed" || row.State == "cancelled" {
		return RealtimeReplayJob{}, ErrConflict
	}
	status := map[string]string{"pause": "paused", "resume": "running", "cancel": "cancelled"}[action]
	if row.DryRun {
		status = map[string]string{"pause": "check_paused", "resume": "checking", "cancel": "cancelled"}[action]
	}
	row, err = queries.UpdateReplayJob(ctx, tx, q.UpdateReplayJobParams{GroupID: row.GroupID, ID: row.ID, State: status, Position: row.Position, Outcomes: row.Outcomes, Delay: 1 / float64(row.Rate)})
	if err != nil {
		return RealtimeReplayJob{}, err
	}
	result, err := replayJobView(row)
	if err != nil {
		return result, err
	}
	return result, tx.Commit(ctx)
}
func (s *PgStore) RunRealtimeReplayJobs(ctx context.Context) error {
	rows, err := q.New().DueReplayJobs(ctx, s.pool, api.RealtimeReplayJobPassMax)
	if err != nil {
		return err
	}
	var failures []error
	for _, row := range rows {
		if err := s.runRealtimeReplayJob(ctx, row); err != nil && !errors.Is(err, ErrNotFound) {
			failures = append(failures, err)
			if ctx.Err() == nil {
				recordErr := q.New().RecordReplayJobError(ctx, s.pool, q.RecordReplayJobErrorParams{ID: row.ID, GroupID: row.GroupID, Delay: api.RealtimeReplayJobErrorBackoffSeconds})
				if recordErr != nil {
					failures = append(failures, recordErr)
				}
			}
		}
	}
	return errors.Join(failures...)
}
func (s *PgStore) runRealtimeReplayJob(ctx context.Context, candidate q.DueReplayJobsRow) error {
	tx, g, err := s.lockRealtimeGroup(ctx, uuid.UUID(candidate.EndpointID.Bytes).String(), candidate.Channel, candidate.Name)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if replayJobUUID(g.id) != candidate.GroupID {
		return nil
	}
	queries := q.New()
	row, err := queries.ReadReplayJob(ctx, tx, q.ReadReplayJobParams{GroupID: candidate.GroupID, ID: candidate.ID})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	eligible, err := queries.ReplayJobEligible(ctx, tx, row.ID)
	if err != nil || !eligible {
		return err
	}
	var items []RealtimeDeadLetterReplaySelection
	var outcomes []RealtimeReplayJobOutcome
	if err = json.Unmarshal(row.Selection, &items); err != nil {
		return err
	}
	if err = json.Unmarshal(row.Outcomes, &outcomes); err != nil {
		return err
	}
	if int(row.Position) >= len(items) {
		return errors.New("realtime replay job position exceeds selection")
	}
	if !row.DryRun {
		if err = expireRealtimeGroupLeases(ctx, tx, g); err != nil {
			return err
		}
	}
	item := items[row.Position]
	seq, err := realtimeGroupSequence(item.Sequence)
	if err != nil {
		return err
	}
	var status string
	if row.DryRun {
		status, err = inspectSelectedRealtimeDeadLetter(ctx, tx, g, seq, item.DeadSince)
	} else {
		status, err = replaySelectedRealtimeDeadLetter(ctx, tx, g, seq, item.DeadSince)
	}
	if err != nil {
		return err
	}
	checkedAt := time.Now().UTC()
	outcomes = append(outcomes, RealtimeReplayJobOutcome{RealtimeDeadLetterReplayOutcome: RealtimeDeadLetterReplayOutcome{Sequence: item.Sequence, Status: status}, CheckedAt: &checkedAt})
	encoded, err := json.Marshal(outcomes)
	if err != nil {
		return err
	}
	row.Position++
	if int(row.Position) == len(items) {
		row.State = "completed"
	}
	_, err = queries.UpdateReplayJob(ctx, tx, q.UpdateReplayJobParams{GroupID: row.GroupID, ID: row.ID, State: row.State, Position: row.Position, Outcomes: encoded, Delay: 1 / float64(row.Rate)})
	if err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func replayJobIDPtr(id pgtype.UUID) *string {
	if !id.Valid {
		return nil
	}
	value := uuid.UUID(id.Bytes).String()
	return &value
}
