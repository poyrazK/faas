package main

import (
	"context"
	"errors"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

type staleUploadReaperStore struct {
	state.Store
	candidate sqlc.ReapExpiredUploadSessionsRow
	readErr   error
	expired   bool
}

func (s *staleUploadReaperStore) ReapExpiredUploadSessions(context.Context) ([]sqlc.ReapExpiredUploadSessionsRow, error) {
	return []sqlc.ReapExpiredUploadSessionsRow{s.candidate}, nil
}

func (s *staleUploadReaperStore) GetUploadSession(ctx context.Context, id string) (sqlc.UploadSession, error) {
	if s.readErr != nil {
		return sqlc.UploadSession{}, s.readErr
	}
	row, err := s.Store.GetUploadSession(ctx, id)
	if s.expired {
		row.ExpiresAt = pgtype.Timestamptz{Time: time.Now().Add(-time.Hour), Valid: true}
	}
	return row, err
}

func TestUploadReaperRechecksCandidateBeforeDeletingSource(t *testing.T) {
	for _, scenario := range []string{"commit completed after scan", "expiry refreshed after scan", "state read failed", "still expired"} {
		t.Run(scenario, func(t *testing.T) {
			t.Setenv("FAAS_SPOOL_ROOT", t.TempDir())
			e := setup(t, api.PlanFree)
			e.do(t, http.MethodPost, "/v1/apps", api.CreateAppRequest{Slug: "reaper-upload"}, nil)
			session := startSession(t, e, "reaper-upload", 4)
			row, err := e.store.GetUploadSession(t.Context(), session.UploadID)
			if err != nil {
				t.Fatal(err)
			}
			stale := &staleUploadReaperStore{Store: e.store, candidate: sqlc.ReapExpiredUploadSessionsRow{ID: row.ID, PartPath: row.PartPath}}
			// The scan result predates acquiring the session lock. Model
			// the state visible only after that lock is acquired.
			if scenario == "commit completed after scan" {
				if _, err := e.store.MarkUploadSessionCommitted(t.Context(), sqlc.MarkUploadSessionCommittedParams{
					ID: row.ID, DeploymentID: pgtype.Text{String: "committed-deployment", Valid: true},
				}); err != nil {
					t.Fatal(err)
				}
			}
			if scenario == "state read failed" {
				stale.readErr = errors.New("database unavailable")
			}
			stale.expired = scenario == "still expired" || scenario == "commit completed after scan"
			e.s.store = stale
			sweepUploadSessions(t.Context(), e.s, e.s.log)
			if scenario == "still expired" {
				if _, err := os.Stat(row.PartPath); !errors.Is(err, os.ErrNotExist) {
					t.Fatalf("expired source was not removed: %v", err)
				}
				current, err := e.store.GetUploadSession(t.Context(), row.ID)
				if err != nil || current.PartPath != "" || current.Status != "expired" {
					t.Fatalf("expired session was not finalized: row=%+v error=%v", current, err)
				}
				return
			}
			if _, err := os.Stat(row.PartPath); err != nil {
				t.Fatalf("reaper deleted source using a stale candidate: %v", err)
			}
			current, err := e.store.GetUploadSession(t.Context(), row.ID)
			if err != nil || current.PartPath != row.PartPath {
				t.Fatalf("reaper cleared source marker: row=%+v error=%v", current, err)
			}
		})
	}
}
