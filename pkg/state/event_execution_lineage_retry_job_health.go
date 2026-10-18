package state

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

type lineageRetryHealthObservation struct {
	id, status                string
	processed, selected       int
	created, activity, retain time.Time
	progress                  *time.Time
}

func lineageRetryHealthBefore(a, b lineageRetryHealthObservation) bool {
	if !a.retain.Equal(b.retain) {
		return a.retain.Before(b.retain)
	}
	if !a.activity.Equal(b.activity) {
		return a.activity.Before(b.activity)
	}
	return a.id < b.id
}
func buildLineageRetryJobHealth(rows []lineageRetryHealthObservation, now time.Time) (*api.EventExecutionLineageRetryJobHealth, error) {
	out := &api.EventExecutionLineageRetryJobHealth{Coverage: "bounded_retained_active_lineage_retry_jobs", CountsComplete: len(rows) <= api.EventExecutionLineageRetryHealthJobsMax, JobLimit: api.EventExecutionLineageRetryHealthJobsMax, InactivityWarningSeconds: int64(api.EventExecutionLineageRetryInactivityWarning / time.Second), RetentionWarningSeconds: int64(api.EventExecutionLineageRetryRetentionWarning / time.Second), Jobs: make([]api.EventExecutionLineageRetryJobHealthSample, 0)}
	if len(rows) > api.EventExecutionLineageRetryHealthJobsMax {
		rows = rows[:api.EventExecutionLineageRetryHealthJobsMax]
	}
	samples := make([]api.EventExecutionLineageRetryJobHealthSample, 0, len(rows))
	for _, row := range rows {
		if row.selected < 1 || row.selected > api.EventExecutionLineageRetryJobMaxCases || row.processed < 0 || row.processed >= row.selected || row.created.IsZero() || row.activity.IsZero() || !row.retain.After(row.created) || (row.status != "pending" && row.status != "paused") {
			return nil, fmt.Errorf("invalid retained lineage retry job metadata")
		}
		sample := api.EventExecutionLineageRetryJobHealthSample{RequestID: row.id, Status: row.status, Processed: row.processed, Selected: row.selected, Remaining: row.selected - row.processed, CreatedAt: row.created, LastActivityAt: row.activity, InactivityAgeSeconds: max(0, now.Sub(row.activity).Seconds()), RetainUntil: row.retain, RetentionSeconds: max(0, row.retain.Sub(now).Seconds()), RetentionRisk: !row.retain.After(now.Add(api.EventExecutionLineageRetryRetentionWarning))}
		if row.progress != nil && !row.progress.IsZero() {
			at := *row.progress
			sample.LastProgressAt = &at
			age := max(0, now.Sub(at).Seconds())
			sample.ProgressAgeSeconds = &age
			sample.ProgressKnown = true
		} else if row.processed == 0 {
			age := max(0, now.Sub(row.created).Seconds())
			sample.ProgressAgeSeconds = &age
			sample.ProgressKnown = true
		}
		sample.StalledPending = row.status == "pending" && now.Sub(row.activity) >= api.EventExecutionLineageRetryInactivityWarning
		out.ObservedJobs++
		out.RemainingCases += int64(sample.Remaining)
		if row.status == "pending" {
			out.PendingJobs++
			out.PendingRemainingCases += int64(sample.Remaining)
			if out.OldestPendingActivityAt == nil || row.activity.Before(*out.OldestPendingActivityAt) {
				at := row.activity
				out.OldestPendingActivityAt = &at
			}
		} else {
			out.PausedJobs++
			out.PausedRemainingCases += int64(sample.Remaining)
		}
		if sample.StalledPending {
			out.StalledPendingJobs++
		}
		if sample.RetentionRisk {
			out.RetentionRiskJobs++
		}
		if !sample.ProgressKnown {
			out.UnknownProgressJobs++
		}
		if out.NextRetainUntil == nil || row.retain.Before(*out.NextRetainUntil) {
			at := row.retain
			out.NextRetainUntil = &at
		}
		samples = append(samples, sample)
	}
	sort.Slice(samples, func(i, j int) bool {
		a, b := samples[i], samples[j]
		if a.RetentionRisk != b.RetentionRisk {
			return a.RetentionRisk
		}
		if a.StalledPending != b.StalledPending {
			return a.StalledPending
		}
		if !a.RetainUntil.Equal(b.RetainUntil) {
			return a.RetainUntil.Before(b.RetainUntil)
		}
		if !a.LastActivityAt.Equal(b.LastActivityAt) {
			return a.LastActivityAt.Before(b.LastActivityAt)
		}
		return a.RequestID < b.RequestID
	})
	if len(samples) > api.EventExecutionLineageRetryHealthSampleMax {
		samples = samples[:api.EventExecutionLineageRetryHealthSampleMax]
	}
	out.Jobs = samples
	return out, nil
}

func observeLineageRetryJobHealth(ctx context.Context, q *sqlc.Queries, db sqlc.DBTX, account, app, sub string, now time.Time) (*api.EventExecutionLineageRetryJobHealth, error) {
	rows, err := q.EventExecutionLineageRetryHealthJobs(ctx, db, sqlc.EventExecutionLineageRetryHealthJobsParams{AccountID: mustPgUUID(account), AppID: mustPgUUID(app), SubscriptionID: mustPgUUID(sub), NowAt: pgtypeFromTime(now), JobLimit: api.EventExecutionLineageRetryHealthJobsMax + 1})
	if err != nil {
		return nil, err
	}
	observations := make([]lineageRetryHealthObservation, 0, len(rows))
	for _, row := range rows {
		observations = append(observations, lineageRetryHealthObservation{id: uuidString(row.RequestID), status: row.State, processed: int(row.Processed), selected: int(row.Selected), created: timeFromPgtype(row.CreatedAt), activity: timeFromPgtype(row.UpdatedAt), retain: timeFromPgtype(row.RetainUntil), progress: timestamptzToTimePtr(row.LastProgressAt)})
	}
	return buildLineageRetryJobHealth(observations, now)
}

func (m *MemStore) observeLineageRetryJobHealthLocked(app, sub string, now time.Time) (*api.EventExecutionLineageRetryJobHealth, error) {
	rows := make([]lineageRetryHealthObservation, 0, api.EventExecutionLineageRetryHealthJobsMax+1)
	for _, job := range m.eventExecutionCoverage[canonicalMemUUID(sub)].jobs {
		if !sameMemUUID(job.AppID, app) || job.CreatedAt.After(now) || !job.RetainUntil.After(now) || (job.Status != "pending" && job.Status != "paused") {
			continue
		}
		row := lineageRetryHealthObservation{id: job.RequestID, status: job.Status, processed: job.Processed, selected: len(job.Items), created: job.CreatedAt, activity: job.UpdatedAt, retain: job.RetainUntil, progress: job.LastProgressAt}
		// Keep the same earliest deadlines as PostgreSQL with bounded memory.
		position := sort.Search(len(rows), func(i int) bool { return lineageRetryHealthBefore(row, rows[i]) })
		if position > api.EventExecutionLineageRetryHealthJobsMax {
			continue
		}
		rows = append(rows, lineageRetryHealthObservation{})
		copy(rows[position+1:], rows[position:])
		rows[position] = row
		if len(rows) > api.EventExecutionLineageRetryHealthJobsMax+1 {
			rows = rows[:api.EventExecutionLineageRetryHealthJobsMax+1]
		}
	}
	return buildLineageRetryJobHealth(rows, now)
}
