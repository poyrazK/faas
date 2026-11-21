// adr: 1139
package state_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestMemDurableCloneCapturesProjectSecrets(t *testing.T) {
	durableCloneCapturesProjectSecrets(t, state.NewMemStore())
}

// A durable clone captures the source environment's shared secrets as sealed
// envelopes instead of refusing the source (ADR-20261104120000 amendment
// 2026-11-21).
func durableCloneCapturesProjectSecrets(t *testing.T, s contractTestStore) {
	t.Helper()
	ctx := context.Background()
	account, project, _, op := cloneBindingFixture(t, s)
	if _, err := s.SetProjectEnvironmentSecret(ctx, state.ProjectEnvironmentSecret{AccountID: account.ID, ProjectID: project.ID,
		EnvironmentSlug: op.SourceEnvironment, Key: "DATABASE_PASSWORD", Ciphertext: []byte("sealed"),
		Kid: "age1qyqszqgpqyqszqgpqyqszqgpqyqszqgpqyqszqgpqyqszqgpqyqs3290gq", ValueHash: "ab"}, 10); err != nil {
		t.Fatal(err)
	}
	lease, err := s.ClaimNextProjectEnvironmentClone(ctx, uuid.NewString(), time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	views, err := s.CaptureProjectEnvironmentCloneWorkloads(ctx, account.ID, project.ID, lease.Operation.ID, lease.Operation.Revision)
	if err != nil || len(views) != 1 {
		t.Fatalf("capture with a shared secret = %d workloads, %v", len(views), err)
	}
}
