// adr: 531
package state_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

type cloneWorkerLeaseTestStore interface {
	state.Store
	state.ProjectEnvironmentCloneOperationStore
	state.ProjectEnvironmentCloneWorkerLeaseStore
	state.ProjectEnvironmentCloneWorkloadStore
}

func TestMemProjectEnvironmentCloneWorkerLeaseOwnership(t *testing.T) {
	projectEnvironmentCloneWorkerLeaseOwnership(t, state.NewMemStore())
}

func projectEnvironmentCloneWorkerLeaseOwnership(t *testing.T, s cloneWorkerLeaseTestStore) {
	t.Helper()
	ctx := context.Background()
	acct, err := s.CreateAccount(ctx, "clone-worker@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	project, err := s.CreateProject(ctx, state.Project{AccountID: acct.ID, Slug: "leased"})
	if err != nil {
		t.Fatal(err)
	}
	app, err := s.CreateApp(ctx, state.App{AccountID: acct.ID, ProjectID: project.ID,
		Slug: "leased-workload", Type: state.AppTypeApp, RAMMB: 256, MaxConcurrency: 1})
	if err != nil {
		t.Fatal(err)
	}
	source, err := s.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage, ImageDigest: "sha256:leased"})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SetDeploymentRootfs(ctx, source.ID, "/leased.ext4", "layers/leased.ext4", 4096); err != nil {
		t.Fatal(err)
	}
	if err := s.MarkDeploymentLive(ctx, source.ID); err != nil {
		t.Fatal(err)
	}
	op, err := s.CreateProjectEnvironmentCloneOperation(ctx, state.ProjectEnvironmentCloneOperation{
		AccountID: acct.ID, ProjectID: project.ID, SourceEnvironment: "production", TargetEnvironment: "stage",
		IdempotencyKey: "leased", SourceRevisionHash: strings.Repeat("a", 64),
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, input := range []struct {
		token string
		ttl   time.Duration
	}{{"", time.Minute}, {uuid.Nil.String(), time.Minute}, {uuid.NewString(), 0}, {uuid.NewString(), time.Nanosecond}} {
		if _, err := s.ClaimNextProjectEnvironmentClone(ctx, input.token, input.ttl); !errors.Is(err, state.ErrInvalidArgument) {
			t.Fatalf("invalid claim = %v", err)
		}
	}
	// Multiple daemon replicas race for the same operation. Exactly one can
	// obtain it; the other workers observe an empty queue until release/expiry.
	start := make(chan struct{})
	claims := make(chan state.ProjectEnvironmentCloneLease, 8)
	errorsCh := make(chan error, 8)
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			claim, err := s.ClaimNextProjectEnvironmentClone(ctx, uuid.NewString(), time.Minute)
			if err != nil {
				errorsCh <- err
				return
			}
			claims <- claim
		}()
	}
	close(start)
	wg.Wait()
	close(claims)
	close(errorsCh)
	if len(claims) != 1 {
		t.Fatalf("successful claims = %d, want 1", len(claims))
	}
	for err := range errorsCh {
		if !errors.Is(err, state.ErrNotFound) {
			t.Fatalf("contending claim = %v", err)
		}
	}
	lease := <-claims
	if lease.Operation.ID != op.ID || lease.Operation.Revision != op.Revision+1 || lease.AttemptCount != 1 || lease.ExpiresAt.IsZero() {
		t.Fatal("claim did not fence the original operation or record its attempt")
	}
	encoded, err := json.Marshal(lease)
	if err != nil || strings.Contains(string(encoded), lease.Token) {
		t.Fatal("worker token appeared in serialized status")
	}
	if _, err := s.AdvanceProjectEnvironmentCloneOperation(ctx, acct.ID, project.ID, op.ID, op.Status, state.CloneOperationCapturing, op.Revision, nil, ""); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("pre-claim revision advanced: %v", err)
	}
	wrongToken := lease
	wrongToken.Token = uuid.NewString()
	if _, err := s.RenewProjectEnvironmentCloneLease(ctx, wrongToken, time.Minute); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("foreign worker renewed lease: %v", err)
	}
	if err := s.ReleaseProjectEnvironmentCloneLease(ctx, wrongToken, 0); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("foreign worker released lease: %v", err)
	}
	crossAccount := lease
	crossAccount.Operation.AccountID = uuid.NewString()
	if _, err := s.RenewProjectEnvironmentCloneLease(ctx, crossAccount, time.Minute); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("cross-account lease read = %v", err)
	}
	renewed, err := s.RenewProjectEnvironmentCloneLease(ctx, lease, time.Microsecond)
	if err != nil || renewed.Operation.Revision != lease.Operation.Revision || renewed.ExpiresAt.Before(lease.ExpiresAt) || renewed.AttemptCount != 1 {
		t.Fatalf("short renewal changed the fence/deadline: %v", err)
	}
	advanced, err := s.AdvanceProjectEnvironmentCloneOperation(ctx, acct.ID, project.ID, op.ID, op.Status, state.CloneOperationCapturing, lease.Operation.Revision, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.RenewProjectEnvironmentCloneLease(ctx, lease, time.Minute); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("renewal ignored a new operation revision: %v", err)
	}
	lease.Operation = advanced
	if err := s.ReleaseProjectEnvironmentCloneLease(ctx, lease, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AdvanceProjectEnvironmentCloneOperation(ctx, acct.ID, project.ID, op.ID, advanced.Status, advanced.Status, advanced.Revision, nil, ""); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("released worker advanced: %v", err)
	}
	// A crashed worker's lease expires on the store clock, not a caller-supplied
	// timestamp. Expiry rejects mutation even before a replacement claims it.
	expiring, err := s.ClaimNextProjectEnvironmentClone(ctx, uuid.NewString(), 100*time.Millisecond)
	if err != nil || expiring.AttemptCount != 2 {
		t.Fatalf("resumed claim = %v", err)
	}
	time.Sleep(120 * time.Millisecond)
	if _, err := s.RenewProjectEnvironmentCloneLease(ctx, expiring, time.Minute); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("expired worker renewed: %v", err)
	}
	if _, err := s.AdvanceProjectEnvironmentCloneOperation(ctx, acct.ID, project.ID, op.ID, expiring.Operation.Status, expiring.Operation.Status, expiring.Operation.Revision, nil, ""); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("expired worker advanced: %v", err)
	}
	if _, err := s.CaptureProjectEnvironmentCloneWorkloads(ctx, acct.ID, project.ID, op.ID, expiring.Operation.Revision); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("expired worker captured artifacts: %v", err)
	}
	replacement, err := s.ClaimNextProjectEnvironmentClone(ctx, uuid.NewString(), time.Minute)
	if err != nil || replacement.AttemptCount != 3 || replacement.Operation.Revision != expiring.Operation.Revision+1 {
		t.Fatalf("takeover = %v", err)
	}
	if err := s.ReleaseProjectEnvironmentCloneLease(ctx, expiring, 0); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("expired worker released replacement: %v", err)
	}
	if _, err := s.AdvanceProjectEnvironmentCloneOperation(ctx, acct.ID, project.ID, op.ID, expiring.Operation.Status, expiring.Operation.Status, expiring.Operation.Revision, nil, ""); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("old worker changed replacement state: %v", err)
	}
	if views, err := s.CaptureProjectEnvironmentCloneWorkloads(ctx, acct.ID, project.ID, op.ID, replacement.Operation.Revision); err != nil || len(views) != 1 || views[0].SourceDeploymentID != source.ID {
		t.Fatalf("replacement could not capture the valid workload: %v", err)
	}
	if err := s.ReleaseProjectEnvironmentCloneLease(ctx, replacement, time.Hour); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ClaimNextProjectEnvironmentClone(ctx, uuid.NewString(), time.Minute); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("delayed retry claimed early: %v", err)
	}
	// A delayed project cannot starve eligible work in a different project.
	otherProject, err := s.CreateProject(ctx, state.Project{AccountID: acct.ID, Slug: "other"})
	if err != nil {
		t.Fatal(err)
	}
	other, err := s.CreateProjectEnvironmentCloneOperation(ctx, state.ProjectEnvironmentCloneOperation{
		AccountID: acct.ID, ProjectID: otherProject.ID, SourceEnvironment: "production", TargetEnvironment: "stage",
		IdempotencyKey: "other", SourceRevisionHash: strings.Repeat("b", 64),
	})
	if err != nil {
		t.Fatal(err)
	}
	otherLease, err := s.ClaimNextProjectEnvironmentClone(ctx, uuid.NewString(), time.Minute)
	if err != nil || otherLease.Operation.ID != other.ID {
		t.Fatalf("due operation was starved: %v", err)
	}
	failed, err := s.AdvanceProjectEnvironmentCloneOperation(ctx, acct.ID, otherProject.ID, other.ID, other.Status, state.CloneOperationFailed, otherLease.Operation.Revision, nil, "provider_unavailable")
	if err != nil {
		t.Fatal(err)
	}
	otherLease.Operation = failed
	if _, err := s.RenewProjectEnvironmentCloneLease(ctx, otherLease, time.Minute); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("failed operation retained worker authority: %v", err)
	}
	if _, err := s.ClaimNextProjectEnvironmentClone(ctx, uuid.NewString(), time.Minute); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("failed operation was automatically re-executed: %v", err)
	}
}
