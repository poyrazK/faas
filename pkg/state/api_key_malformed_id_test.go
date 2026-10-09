package state_test

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

type apiKeyLookupStore interface {
	GetAPIKey(context.Context, string, string) (state.APIKey, error)
	MarkAPIKeyRevoked(context.Context, string, string) (state.APIKey, error)
	GetOrgAPIKey(context.Context, string, string) (state.APIKey, error)
	RevokeOrgAPIKey(context.Context, string, string) (state.APIKey, error)
}

// production-us hunt #7 (H5-70): `gregale keys rotate h7-lifecycle` (a
// label, not a key id) answered "Briefly at capacity / could not load key":
// PgStore sent the label to a uuid column and the handler mapped the
// database error to a capacity problem. A malformed id is an unknown key.
func runMalformedAPIKeyIDScenario(t *testing.T, ctx context.Context, s apiKeyLookupStore, accountID string) {
	t.Helper()
	orgID := uuid.NewString()
	for _, id := range []string{"h7-lifecycle", "fp_live_8f9e45fe", ""} {
		if _, err := s.GetAPIKey(ctx, accountID, id); !errors.Is(err, state.ErrNotFound) {
			t.Errorf("GetAPIKey(%q) err = %v, want ErrNotFound", id, err)
		}
		if _, err := s.MarkAPIKeyRevoked(ctx, accountID, id); !errors.Is(err, state.ErrNotFound) {
			t.Errorf("MarkAPIKeyRevoked(%q) err = %v, want ErrNotFound", id, err)
		}
		if _, err := s.GetOrgAPIKey(ctx, orgID, id); !errors.Is(err, state.ErrNotFound) {
			t.Errorf("GetOrgAPIKey(%q) err = %v, want ErrNotFound", id, err)
		}
		if _, err := s.RevokeOrgAPIKey(ctx, orgID, id); !errors.Is(err, state.ErrNotFound) {
			t.Errorf("RevokeOrgAPIKey(%q) err = %v, want ErrNotFound", id, err)
		}
	}
}

func TestMem_MalformedAPIKeyIDIsNotFound(t *testing.T) {
	m := state.NewMemStore()
	ctx := context.Background()
	acct, err := m.CreateAccount(ctx, "keys-"+uuid.NewString()+"@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	runMalformedAPIKeyIDScenario(t, ctx, m, acct.ID)
}

func TestPg_MalformedAPIKeyIDIsNotFound(t *testing.T) {
	s, ctx := pgStore(t)
	acct, _ := seedPgAccountAndApp(t, s, ctx)
	runMalformedAPIKeyIDScenario(t, ctx, s, acct.ID)
}
