//go:build !no_pg

package state_test

import (
	"context"
	"errors"
	"github.com/onebox-faas/faas/pkg/state"
	"testing"
	"time"
)

func TestPgStandardRuntimeQualificationLifecycle(t *testing.T) {
	s, _ := state.RuntimeCapturePGStore(t)
	standardRuntimeQualificationLifecycle(t, s)
}

func TestPgStandardRuntimeQualificationScanSelection(t *testing.T) {
	s, _ := state.RuntimeCapturePGStore(t)
	standardRuntimeQualificationScanSelection(t, s)
}

func TestPgStandardRuntimeQualificationNonwaiting(t *testing.T) {
	s, pool := state.RuntimeCapturePGStore(t)
	ins, receipt := state.IssueConsumedNativeFixture(t, s)
	ins, err := s.PublishInstanceApplicationStandardRuntime(t.Context(), ins.State, state.StateRunning, receipt)
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
			if q.Qualified || errors.Is(err, context.DeadlineExceeded) || err != nil && !errors.Is(err, state.ErrApplicationStandardRuntimeBusy) {
				t.Fatalf("qualification waited or fabricated success: %+v %v", q, err)
			}
		})
	}
}
