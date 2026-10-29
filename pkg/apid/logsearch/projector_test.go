package logsearch

// adr: 20261018083536 — apid projects deployment_audit rows and finished
// builds into the ledger: trailing commit order, once per row, retrying a
// failed insert on the next tick.

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

type fakeProjectorStore struct {
	audit      []state.LedgerDeploymentAudit
	builds     []state.LedgerBuildOutcome
	inserted   []state.LogEvent
	failInsert error
}

func (f *fakeProjectorStore) ListDeploymentAuditForLedger(_ context.Context, afterAt time.Time, afterID int64, until time.Time, limit int) ([]state.LedgerDeploymentAudit, error) {
	var out []state.LedgerDeploymentAudit
	for _, row := range f.audit {
		if (row.At.After(afterAt) || row.At.Equal(afterAt) && row.ID > afterID) && row.At.Before(until) && len(out) < limit {
			out = append(out, row)
		}
	}
	return out, nil
}

func (f *fakeProjectorStore) ListBuildOutcomesForLedger(_ context.Context, afterAt time.Time, afterID string, until time.Time, limit int) ([]state.LedgerBuildOutcome, error) {
	var out []state.LedgerBuildOutcome
	for _, row := range f.builds {
		if (row.FinishedAt.After(afterAt) || row.FinishedAt.Equal(afterAt) && row.BuildID > afterID) && row.FinishedAt.Before(until) && len(out) < limit {
			out = append(out, row)
		}
	}
	return out, nil
}

func (f *fakeProjectorStore) InsertLogEvent(_ context.Context, event state.LogEvent) (state.LogEvent, error) {
	if f.failInsert != nil {
		return state.LogEvent{}, f.failInsert
	}
	f.inserted = append(f.inserted, event)
	return event, nil
}

func TestProjectorTrailsCommitOrderAndProjectsOnce(t *testing.T) {
	now := time.Date(2026, 10, 22, 9, 0, 0, 0, time.UTC)
	store := &fakeProjectorStore{
		audit: []state.LedgerDeploymentAudit{
			{ID: 1, At: now.Add(-20 * time.Minute), AccountID: "acct", AppID: "app", DeploymentID: "dep", Kind: state.DeployCreated}, // before the lookback
			{ID: 2, At: now.Add(-time.Minute), AccountID: "acct", AppID: "app", DeploymentID: "dep", Kind: state.DeployRolledBack, Data: json.RawMessage(`{"trigger_kind":"error_rate"}`)},
			{ID: 3, At: now.Add(-5 * time.Second), AccountID: "acct", AppID: "app", DeploymentID: "dep", Kind: state.DeployTrafficChanged, Data: json.RawMessage(`{"from_percent":10,"to_percent":50}`)},
		},
		builds: []state.LedgerBuildOutcome{{
			BuildID: "b-1", AccountID: "acct", AppID: "app", DeploymentID: "dep", Kind: "railpack",
			Status: state.BuildFailed, FailureClass: state.FailureUserError,
			StartedAt: now.Add(-3 * time.Minute), FinishedAt: now.Add(-2 * time.Minute), DeploymentError: "npm ERR! missing script: build",
		}},
	}
	clock := now
	p := NewProjector(store, func() time.Time { return clock }, nil, nil)
	if err := p.Tick(context.Background()); err != nil {
		t.Fatalf("tick: %v", err)
	}
	if len(store.inserted) != 2 {
		t.Fatalf("first tick inserted %+v, want the rollback and the build (the traffic change is inside the commit lag)", store.inserted)
	}
	rollback, build := store.inserted[0], store.inserted[1]
	if rollback.Source != state.LogEventSourceDeploy || rollback.SourceEventID != "audit:2" || rollback.Level != "error" ||
		rollback.Message != "deploy rolled back (trigger: error_rate)" || rollback.AccountID != "acct" || rollback.DeploymentID != "dep" {
		t.Fatalf("rollback event = %+v", rollback)
	}
	if build.Source != state.LogEventSourceBuild || build.SourceEventID != "build:b-1" || build.Level != "error" ||
		build.Message != "build failed (user_error) after 1m0s: npm ERR! missing script: build [railpack]" {
		t.Fatalf("build event = %+v", build)
	}

	clock = now.Add(api.LogProjectionInterval)
	if err := p.Tick(context.Background()); err != nil {
		t.Fatalf("second tick: %v", err)
	}
	if len(store.inserted) != 3 || store.inserted[2].Message != "deploy traffic changed from 10% to 50%" || store.inserted[2].Level != "info" {
		t.Fatalf("second tick inserted %+v, want only the traffic change", store.inserted[2:])
	}
}

func TestProjectorRetriesAFailedInsert(t *testing.T) {
	now := time.Date(2026, 10, 22, 9, 0, 0, 0, time.UTC)
	store := &fakeProjectorStore{
		audit:      []state.LedgerDeploymentAudit{{ID: 7, At: now.Add(-time.Minute), AccountID: "acct", AppID: "app", Kind: state.DeployHealthProbeFailed}},
		failInsert: errors.New("connection reset"),
	}
	p := NewProjector(store, func() time.Time { return now }, nil, nil)
	if err := p.Tick(context.Background()); err == nil || !strings.Contains(err.Error(), "audit:7") {
		t.Fatalf("tick error = %v, want the failed insert", err)
	}
	store.failInsert = nil
	if err := p.Tick(context.Background()); err != nil {
		t.Fatalf("retry tick: %v", err)
	}
	if len(store.inserted) != 1 || store.inserted[0].SourceEventID != "audit:7" || store.inserted[0].Level != "warn" {
		t.Fatalf("inserted %+v, want the retried health-probe event", store.inserted)
	}
}

func TestBuildEventByStatus(t *testing.T) {
	finished := time.Date(2026, 10, 22, 9, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		row          state.LedgerBuildOutcome
		level, wants string
	}{
		{state.LedgerBuildOutcome{Status: state.BuildSucceeded, StartedAt: finished.Add(-72 * time.Second), FinishedAt: finished, Kind: "dockerfile"}, "info", "build succeeded after 1m12s [dockerfile]"},
		{state.LedgerBuildOutcome{Status: state.BuildCancelled, FinishedAt: finished}, "warn", "build cancelled"},
		{state.LedgerBuildOutcome{Status: state.BuildFailed, FinishedAt: finished, DeploymentError: strings.Repeat("é", api.LogProjectionBuildErrorBytes)}, "error", "build failed: é"},
	} {
		event := buildEvent(tc.row)
		if event.Level != tc.level || !strings.HasPrefix(event.Message, tc.wants) || len(event.Message) > len("build failed: ")+api.LogProjectionBuildErrorBytes {
			t.Errorf("%s: event = %q (%s), want prefix %q at level %s", tc.row.Status, event.Message, event.Level, tc.wants, tc.level)
		}
	}
}
