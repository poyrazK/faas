// adr: 1139
package state_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestMemDurableCloneRefusesProjectSecrets(t *testing.T) {
	durableCloneRefusesProjectSecrets(t, state.NewMemStore())
}

// A durable clone cannot carry shared secrets yet, so its capture refuses a
// source environment that has any instead of cloning services that would
// boot without them.
func durableCloneRefusesProjectSecrets(t *testing.T, s contractTestStore) {
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
	if _, err := s.CaptureProjectEnvironmentCloneWorkloads(ctx, account.ID, project.ID, lease.Operation.ID, lease.Operation.Revision); !errors.Is(err, state.ErrProjectSecretCloneTransferUnsupported) {
		t.Fatalf("capture with a shared secret = %v, want ErrProjectSecretCloneTransferUnsupported", err)
	}
}
