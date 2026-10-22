package operatorobs

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// A narrow fake lets these tests distinguish lookup and history failures from
// genuine absence without generating wrappers for the full state.Store.
type evidenceReader struct {
	dep                   state.Deployment
	app                   state.App
	run                   state.JobRun
	node                  state.ComputeNode
	rows                  []api.ObsIncidentEvidenceEntry
	lookupErr, historyErr error
	requestedLimit        int
}

func (s *evidenceReader) DeploymentByID(context.Context, string) (state.Deployment, error) {
	return s.dep, s.lookupErr
}
func (s *evidenceReader) AppByID(context.Context, string) (state.App, error) {
	return s.app, s.lookupErr
}
func (s *evidenceReader) JobRunGetByID(context.Context, string) (state.JobRun, error) {
	return s.run, s.lookupErr
}
func (s *evidenceReader) ComputeNodeByID(context.Context, string) (state.ComputeNode, error) {
	return s.node, s.lookupErr
}
func (s *evidenceReader) OperatorIncidentEvidence(_ context.Context, _ string, _ time.Time, limit int) ([]api.ObsIncidentEvidenceEntry, error) {
	s.requestedLimit = limit
	return s.rows, s.historyErr
}

func TestEvidenceResourceStatesAndHistoryFailures(t *testing.T) {
	now := time.Now().UTC()
	id := uuid.NewString()
	for _, tc := range []struct {
		name, kind, status, severity string
		reader                       evidenceReader
		wantErr                      error
	}{
		{name: "failed deployment", kind: "deployment", status: "failed", severity: "error", reader: evidenceReader{dep: state.Deployment{Status: state.DeployFailed, ErrorCode: "image_not_found"}}},
		{name: "active deployment", kind: "deployment", status: "building", severity: "warning", reader: evidenceReader{dep: state.Deployment{Status: state.DeployBuilding}}},
		{name: "recovered deployment", kind: "deployment", status: "live", severity: "info", reader: evidenceReader{dep: state.Deployment{Status: state.DeployLive}}},
		{name: "deleted deployment", kind: "deployment", reader: evidenceReader{dep: state.Deployment{DeletedAt: &now}}, wantErr: state.ErrNotFound},
		{name: "deleted app", kind: "deployment", reader: evidenceReader{app: state.App{Status: state.AppDeleted}}, wantErr: state.ErrNotFound},
		{name: "missing resource", kind: "job_run", reader: evidenceReader{lookupErr: state.ErrNotFound}, wantErr: state.ErrNotFound},
		{name: "failed job", kind: "job_run", status: "dead_letter", severity: "error", reader: evidenceReader{run: state.JobRun{AggregateStatus: "dead_letter", FinishedAt: &now}}},
		{name: "stuck job", kind: "job_run", status: "running", severity: "error", reader: evidenceReader{run: state.JobRun{AggregateStatus: "running", CreatedAt: now.Add(-time.Hour)}}},
		{name: "new job", kind: "job_run", status: "queued", severity: "warning", reader: evidenceReader{run: state.JobRun{AggregateStatus: "queued", CreatedAt: now}}},
		{name: "stale node", kind: "compute_node", status: "stale", severity: "warning", reader: evidenceReader{node: state.ComputeNode{Active: true, Lifecycle: state.NodeLifecycleActive, LastHeartbeatAt: now.Add(-time.Hour)}}},
		{name: "inactive node", kind: "compute_node", status: "draining", severity: "error", reader: evidenceReader{node: state.ComputeNode{Lifecycle: state.NodeLifecycleDraining, DrainInitiatedAt: &now}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := Evidence(context.Background(), &tc.reader, nil, tc.kind, id, 1, now)
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("error=%v want=%v", err, tc.wantErr)
			}
			if err == nil && (got.Incident.Status != tc.status || got.Incident.Severity != tc.severity || tc.reader.requestedLimit != 2) {
				t.Fatalf("evidence=%+v requestedLimit=%d", got, tc.reader.requestedLimit)
			}
		})
	}
	boom := errors.New("history database unavailable")
	reader := &evidenceReader{historyErr: boom}
	if _, err := Evidence(context.Background(), reader, nil, "deployment", id, 1, now); !errors.Is(err, boom) {
		t.Fatalf("history failure became success: %v", err)
	}
}

func TestEvidenceTruncationAndTraceValidation(t *testing.T) {
	id := uuid.NewString()
	reader := &evidenceReader{rows: []api.ObsIncidentEvidenceEntry{
		{ID: "first", TraceID: "11111111111111111111111111111111"},
		{ID: "second", TraceID: "../../private"}, {ID: "third"},
	}}
	got, err := Evidence(context.Background(), reader, nil, "deployment", id, 2, time.Now())
	if err != nil || len(got.History) != 2 || !got.Truncated || got.History[0].TracePath == "" || got.History[1].TraceID != "" {
		t.Fatalf("evidence=%+v error=%v", got, err)
	}
	for _, tc := range []struct {
		kind, id string
		limit    int
	}{
		{"deployment", "invalid", 1}, {"unknown", id, 1}, {"platform_alert", "not-a-hash", 1}, {"deployment", id, 0}, {"deployment", id, 101},
	} {
		if _, err := Evidence(context.Background(), reader, nil, tc.kind, tc.id, tc.limit, time.Now()); !errors.Is(err, ErrInvalidReference) {
			t.Errorf("accepted invalid reference or limit %+v: %v", tc, err)
		}
	}
}
