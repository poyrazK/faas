package accounttest

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/auth"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestInitialPasswordCommitsWithAccountAndPersonalOrganization(t *testing.T) {
	stores(t, func(t *testing.T, store state.Store) {
		params := state.CreateAccountWithPersonalOrgParams{Email: uuid.NewString() + "@example.com", Plan: api.PlanFree,
			RequireEmailVerification: true, InitialPasswordHash: auth.DummyPHC}
		created, err := store.CreateAccountWithPersonalOrg(t.Context(), params)
		if err != nil {
			t.Fatal(err)
		}
		hash, err := store.AccountPasswordByAccountID(t.Context(), created.Account.ID)
		if err != nil || hash != params.InitialPasswordHash || created.Account.EmailVerified() {
			t.Fatal("credential or verification policy missing", err)
		}
		member, err := store.OrgMemberByAccount(t.Context(), created.PersonalOrg.ID, created.Account.ID)
		if err != nil || member.Role != state.OrgRoleOwner {
			t.Fatal("personal owner membership missing", err)
		}
		params.InitialPasswordHash = "another hash"
		if _, err := store.CreateAccountWithPersonalOrg(t.Context(), params); !errors.Is(err, state.ErrConflict) {
			t.Fatal("duplicate signup did not conflict", err)
		}
		hash, err = store.AccountPasswordByAccountID(t.Context(), created.Account.ID)
		if err != nil || hash != auth.DummyPHC {
			t.Fatal("duplicate signup replaced the first password", err)
		}
		encoded, err := json.Marshal(params)
		if err != nil || strings.Contains(string(encoded), params.InitialPasswordHash) || strings.Contains(string(encoded), "InitialPasswordHash") {
			t.Fatal("creation parameters exposed the credential", err)
		}
	})
}

func TestCanceledAccountCreationLeavesEmailAvailable(t *testing.T) {
	stores(t, func(t *testing.T, store state.Store) {
		params := state.CreateAccountWithPersonalOrgParams{Email: uuid.NewString() + "@example.com", Plan: api.PlanFree,
			RequireEmailVerification: true, InitialPasswordHash: auth.DummyPHC}
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		if _, err := store.CreateAccountWithPersonalOrg(ctx, params); !errors.Is(err, context.Canceled) {
			t.Fatal("canceled creation", err)
		}
		if _, err := store.AccountByEmail(t.Context(), params.Email); !errors.Is(err, state.ErrNotFound) {
			t.Fatal("canceled signup reserved the address", err)
		}
		if _, err := store.CreateAccountWithPersonalOrg(t.Context(), params); err != nil {
			t.Fatal("retry after cancellation failed", err)
		}
	})
}

func TestPostgresPasswordFailureRollsBackWholeSignup(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	store := state.NewPgStore(pool)
	params := state.CreateAccountWithPersonalOrgParams{Email: uuid.NewString() + "@example.com", Plan: api.PlanFree,
		RequireEmailVerification: true, InitialPasswordHash: auth.DummyPHC}
	const failPassword = `CREATE FUNCTION reject_signup_password() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN RAISE EXCEPTION 'test credential storage failure'; END $$;
CREATE TRIGGER reject_signup_password BEFORE INSERT ON account_passwords FOR EACH ROW EXECUTE FUNCTION reject_signup_password();`
	if _, err := pool.Exec(t.Context(), failPassword); err != nil {
		t.Fatal(err)
	}
	counts := func() [4]int64 {
		t.Helper()
		var result [4]int64
		if err := pool.QueryRow(t.Context(), `SELECT (SELECT count(*) FROM accounts),(SELECT count(*) FROM orgs),
 (SELECT count(*) FROM org_memberships),(SELECT count(*) FROM account_passwords)`).Scan(&result[0], &result[1], &result[2], &result[3]); err != nil {
			t.Fatal(err)
		}
		return result
	}
	before := counts()
	if _, err := store.CreateAccountWithPersonalOrg(t.Context(), params); err == nil {
		t.Fatal("credential failure did not fail creation")
	}
	if got := counts(); got != before {
		t.Fatalf("signup leaked rows after rollback: before=%v after=%v", before, got)
	}
	if _, err := store.AccountByEmail(t.Context(), params.Email); !errors.Is(err, state.ErrNotFound) {
		t.Fatal("failed signup reserved the address", err)
	}
	if _, err := pool.Exec(t.Context(), `DROP TRIGGER reject_signup_password ON account_passwords`); err != nil {
		t.Fatal(err)
	}
	created, err := store.CreateAccountWithPersonalOrg(t.Context(), params)
	if err != nil {
		t.Fatal("same-email retry after storage recovery", err)
	}
	if hash, err := store.AccountPasswordByAccountID(t.Context(), created.Account.ID); err != nil || hash != params.InitialPasswordHash {
		t.Fatal("retry did not initialize the credential", err)
	}
}

func TestGraceWindowUpdateRequiresAnExistingAccount(t *testing.T) {
	stores(t, func(t *testing.T, store state.Store) {
		days := 0
		if err := store.SetAccountKeyGraceWindow(t.Context(), uuid.NewString(), &days); !errors.Is(err, state.ErrNotFound) {
			t.Fatal("missing account update falsely succeeded", err)
		}
	})
}
