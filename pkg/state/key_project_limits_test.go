package state_test

import (
	"context"
	"crypto/sha256"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/state"
)

// adr: 20261104085712 — a key's project limit is set once and read back; a
// key without one is unlimited; a sign-in's code remembers the projects chosen
// for it until it is claimed.
func exerciseKeyProjectLimits(t *testing.T, store contractTestStore) {
	t.Helper()
	ctx := context.Background()
	suffix := time.Now().Format("150405.000000")
	acct, err := store.CreateAccount(ctx, "key-projects-"+suffix+"@example.com", api.PlanPro)
	if err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}
	_, hash, err := api.GenerateAPIKey()
	if err != nil {
		t.Fatal(err)
	}
	key, err := store.CreateAPIKey(ctx, acct.ID, hash, "agent-"+suffix, []string{api.ScopeAgent, api.ScopeAppsRead})
	if err != nil {
		t.Fatalf("CreateAPIKey: %v", err)
	}
	if ids, limited, err := store.APIKeyProjectLimit(ctx, key.ID); err != nil || limited || len(ids) != 0 {
		t.Fatalf("limit of a fresh key = %v, %v, %v; want unlimited", ids, limited, err)
	}
	projects := []string{uuid.NewString(), uuid.NewString()}
	if err := store.SetAPIKeyProjectLimit(ctx, key.ID, nil); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("an empty limit = %v, want ErrConflict", err)
	}
	if err := store.SetAPIKeyProjectLimit(ctx, key.ID, projects); err != nil {
		t.Fatalf("SetAPIKeyProjectLimit: %v", err)
	}
	if ids, limited, err := store.APIKeyProjectLimit(ctx, key.ID); err != nil || !limited || !slices.Equal(ids, projects) {
		t.Fatalf("limit = %v, %v, %v; want %v", ids, limited, err, projects)
	}
	if err := store.SetAPIKeyProjectLimit(ctx, key.ID, projects[:1]); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("changing a limit = %v, want ErrConflict", err)
	}

	sum := sha256.Sum256([]byte("key-projects-code-" + suffix))
	if err := store.IssueCliAuthCode(ctx, sum[:], time.Now().Add(time.Minute)); err != nil {
		t.Fatalf("IssueCliAuthCode: %v", err)
	}
	if ids, limited, err := store.CliAuthCodeProjects(ctx, sum[:]); err != nil || limited || ids != nil {
		t.Fatalf("projects of a fresh code = %v, %v, %v; want every project", ids, limited, err)
	}
	if err := store.SetCliAuthCodeProjects(ctx, sum[:], projects); err != nil {
		t.Fatalf("SetCliAuthCodeProjects: %v", err)
	}
	if err := store.ClaimCliAuthCode(ctx, sum[:], acct.ID); err != nil {
		t.Fatalf("ClaimCliAuthCode: %v", err)
	}
	if err := store.SetCliAuthCodeProjects(ctx, sum[:], nil); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("changing a claimed code's projects = %v, want ErrNotFound", err)
	}
	if ids, limited, err := store.CliAuthCodeProjects(ctx, sum[:]); err != nil || !limited || !slices.Equal(ids, projects) {
		t.Fatalf("projects of the claimed code = %v, %v, %v; want %v", ids, limited, err, projects)
	}
}

func TestMemStoreKeyProjectLimits(t *testing.T) {
	exerciseKeyProjectLimits(t, state.NewMemStore())
}

func TestPgStoreKeyProjectLimits(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	if err := db.MigrateUp(context.Background(), pool); err != nil {
		t.Fatalf("db.MigrateUp: %v", err)
	}
	exerciseKeyProjectLimits(t, state.NewPgStore(pool))
}
