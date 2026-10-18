//go:build !no_pg

package state

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestPgStandardRuntimeQualificationLifecycle(t *testing.T) {
	s, _ := runtimeCapturePGStore(t)
	standardRuntimeQualificationLifecycle(t, s)
}

func TestPgStandardRuntimeQualificationScanSelection(t *testing.T) {
	s, _ := runtimeCapturePGStore(t)
	standardRuntimeQualificationScanSelection(t, s)
}

func TestPgStandardRuntimeQualificationNonwaiting(t *testing.T) {
	s, pool := runtimeCapturePGStore(t)
	ins, receipt := issueConsumedNativeFixture(t, s)
	ins, err := s.PublishInstanceApplicationStandardRuntime(t.Context(), ins.State, StateRunning, receipt)
	if err != nil {
		t.Fatal(err)
	}
	app, err := s.AppByID(t.Context(), ins.AppID)
	if err != nil {
		t.Fatal(err)
	}
	for _, target := range []string{"apps", "instances", "deployments"} {
		t.Run(target, func(t *testing.T) {
			tx, err := pool.Begin(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback(t.Context())
			id := app.ID
			if target == "instances" {
				id = ins.ID
			} else if target == "deployments" {
				id = ins.DeploymentID
			}
			if _, err := tx.Exec(t.Context(), "SELECT id FROM "+target+" WHERE id=$1 FOR UPDATE", id); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(t.Context(), time.Second)
			defer cancel()
			q, err := s.GetInstanceApplicationStandardRuntimeQualification(ctx, app.OrgID, app.ID, ins.ID)
			if q.Qualified || errors.Is(err, context.DeadlineExceeded) || err != nil && !errors.Is(err, ErrApplicationStandardRuntimeBusy) {
				t.Fatalf("qualification waited or fabricated success: %+v %v", q, err)
			}
		})
	}
}
