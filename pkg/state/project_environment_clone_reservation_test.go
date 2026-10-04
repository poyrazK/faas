package state_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// ADR-581: external preparation reserves the target across every creation
// entry point, and a stale worker cannot commit configuration for that target.
func TestMemProjectEnvironmentCloneReservationOwnership(t *testing.T) {
	projectEnvironmentCloneReservationOwnership(t, state.NewMemStore())
}

type cloneReservationStore interface {
	state.Store
	state.ProjectEnvironmentCloneOperationStore
	CloneProjectEnvironment(context.Context, state.ProjectEnvironmentClone, api.Limits) (state.ProjectEnvironment, state.ProjectEnvironmentCloneResult, error)
}

func projectEnvironmentCloneReservationOwnership(t *testing.T, s cloneReservationStore) {
	t.Helper()
	ctx := context.Background()
	acct, err := s.CreateAccount(ctx, "reservation@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	project, err := s.CreateProject(ctx, state.Project{AccountID: acct.ID, Slug: "reserved"})
	if err != nil {
		t.Fatal(err)
	}
	input := state.ProjectEnvironmentCloneOperation{AccountID: acct.ID, ProjectID: project.ID,
		SourceEnvironment: "production", TargetEnvironment: "stage", IdempotencyKey: "create", SourceRevisionHash: strings.Repeat("a", 64)}
	// Materialization now requires a frozen flag/config capture, even when the
	// source has no configured flags. Give this reservation contract a workload.
	app, err := s.CreateApp(ctx, state.App{AccountID: acct.ID, ProjectID: project.ID, Slug: "reserved-app", Type: state.AppTypeApp, RAMMB: 256, MaxConcurrency: 1})
	if err != nil {
		t.Fatal(err)
	}
	deployment, err := s.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Scope: "production", Kind: state.DeploymentKindImage, ImageDigest: "sha256:reserved"})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SetDeploymentRootfs(ctx, deployment.ID, "/reserved.ext4", "layers/reserved.ext4", 4096); err != nil {
		t.Fatal(err)
	}
	if err := s.MarkDeploymentLive(ctx, deployment.ID); err != nil {
		t.Fatal(err)
	}
	op, err := s.CreateProjectEnvironmentCloneOperation(ctx, input)
	if err != nil {
		t.Fatal(err)
	}
	clone := state.ProjectEnvironmentClone{AccountID: acct.ID, ProjectID: project.ID, SourceSlug: "production", TargetSlug: "stage"}
	if _, err := s.CreateProjectEnvironment(ctx, state.ProjectEnvironment{AccountID: acct.ID, ProjectID: project.ID, Slug: "stage"}); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("ordinary create stole reservation: %v", err)
	}
	if _, _, err := s.CloneProjectEnvironment(ctx, clone, api.MustLimitsFor(acct.Plan)); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("partial clone stole reservation: %v", err)
	}
	clone.CloneOperationID, clone.CloneOperationRevision = op.ID, op.Revision
	if _, _, err := s.CloneProjectEnvironment(ctx, clone, api.MustLimitsFor(acct.Plan)); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("pending owner materialized target: %v", err)
	}
	lease, err := s.(state.ProjectEnvironmentCloneWorkerLeaseStore).ClaimNextProjectEnvironmentClone(ctx, uuid.NewString(), time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	op = lease.Operation
	for _, phase := range []string{state.CloneOperationCapturing, state.CloneOperationCopying} {
		op, err = s.AdvanceProjectEnvironmentCloneOperation(ctx, acct.ID, project.ID, op.ID, op.Status, phase, op.Revision, nil, "")
		if err != nil {
			t.Fatal(err)
		}
		if phase == state.CloneOperationCapturing {
			if _, err := s.(state.ProjectEnvironmentCloneWorkloadStore).CaptureProjectEnvironmentCloneWorkloads(ctx, acct.ID, project.ID, op.ID, op.Revision); err != nil {
				t.Fatal(err)
			}
		}
	}
	if _, _, err := s.CloneProjectEnvironment(ctx, clone, api.MustLimitsFor(acct.Plan)); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("stale worker materialized target: %v", err)
	}
	clone.CloneOperationRevision = op.Revision
	clone.ShareResources = true
	if _, _, err := s.CloneProjectEnvironment(ctx, clone, api.MustLimitsFor(acct.Plan)); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("full owner shared resources: %v", err)
	}
	clone.ShareResources = false
	env, _, err := s.CloneProjectEnvironment(ctx, clone, api.MustLimitsFor(acct.Plan))
	if err != nil || env.Slug != "stage" {
		t.Fatalf("owner clone = %+v, %v", env, err)
	}
	if replay, err := s.CreateProjectEnvironmentCloneOperation(ctx, input); err != nil || replay.ID != op.ID {
		t.Fatalf("replay after materialization = %+v, %v", replay, err)
	}
	// Reserving and ordinary creation run against different tables in PgStore.
	// Their shared project lock must make exactly one of them win.
	for i := 0; i < 12; i++ {
		slug := fmt.Sprintf("race-%d", i)
		racing := input
		racing.TargetEnvironment, racing.IdempotencyKey = slug, slug
		start := make(chan struct{})
		var wg sync.WaitGroup
		var createErr, reserveErr error
		wg.Add(2)
		go func() {
			defer wg.Done()
			<-start
			_, createErr = s.CreateProjectEnvironment(ctx, state.ProjectEnvironment{AccountID: acct.ID, ProjectID: project.ID, Slug: slug})
		}()
		go func() {
			defer wg.Done()
			<-start
			_, reserveErr = s.CreateProjectEnvironmentCloneOperation(ctx, racing)
		}()
		close(start)
		wg.Wait()
		if (createErr == nil) == (reserveErr == nil) {
			t.Fatalf("race %d create=%v reserve=%v", i, createErr, reserveErr)
		}
		if createErr != nil && !errors.Is(createErr, state.ErrConflict) {
			t.Fatal(createErr)
		}
		if reserveErr != nil && !errors.Is(reserveErr, state.ErrConflict) {
			t.Fatal(reserveErr)
		}
	}
}
