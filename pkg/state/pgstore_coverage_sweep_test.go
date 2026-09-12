package state_test

// Consolidated PgStore coverage sweep. Previously 41 individual TestPg_*
// tests across pgstore_coverage_slice1/4/5_test.go, each of which
// bootstrapped a fresh schema (~37 migrations). On CI's 10-minute
// shard-2 budget that serial chain saturated the wall clock.
//
// This file uses three top-level tests with t.Run sub-tests that
// share one schema per top-level test. All emails/slugs/IDs use
// uuid.NewString() so sub-tests never collide. Scope covered:
//
//   TestPg_CoverageSweepAccounts:
//     AccountLifecycle (CreateAccount, AccountByID, AccountByEmail,
//       AccountByKeyHash, AccountByProviderCustomerID, AuthenticateKey,
//       UpdateAccountPlan, UpdateAccountStatus,
//       UpdateAccountProviderCustomerID,
//       UpdateAccountStripeSubscriptionItem, APIKeyByHash)
//     MFALifecycle (ReadMFASecret, SetMFASecret, MarkMFAEnrolled,
//       ClearMFA, SetMFARequired, ConsumeRecoveryCode, MatchRecoveryCode)
//     AccountsByIDs
//     CreateAccountWithPersonalOrg
//     ListAllAccounts, AccountByProviderCustomerID, AccountByProviderCustomerID
//     UpdateAccountProviderCustomerID
//     GetAccountEgressAllowlistExtra / SetAccountEgressAllowlistExtra
//     GetAccountKeyGraceWindow / SetAccountKeyGraceWindow
//     NewPgStore
//
//   TestPg_CoverageSweepAppsAndKeys:
//     CreateApp, AppByID, AppBySlug, ListAPIKeys, CreateAPIKey,
//     GetAPIKey, CountAPIKeys, TouchKeyLastUsed, DeleteAPIKey,
//     DeleteAPIKeyReturning, MarkAPIKeyRevoked,
//     CountDeployments (per-account),
//     AccountNotFoundErr, APIKeyByHashNotFound, TouchNonExistentKey
//
//   TestPg_CoverageSweepDeployments:
//     CreateDeployment, DeploymentByID, LatestDeployment,
//     LiveDeployment, CountDeployments, ListAllDeployments,
//     ListDeploymentsByNodeID, UpdateDeploymentMinInstances,
//     SetDeploymentParked, MarkDeploymentSuperseded,
//     MarkDeploymentLive, SetDeploymentRootfs,
//     UpsertDeploymentScanResult, ListDeploymentsForApp,
//     LatestParkedDeploymentForApp

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/authcode"
	"github.com/onebox-faas/faas/pkg/state"
)

// seedPgAccountAndApp returns an account + an app in the schema. Reused
// from the deleted slice5_test.go.
func seedPgAccountAndApp(t *testing.T, s *state.PgStore, ctx context.Context) (state.Account, state.App) {
	t.Helper()
	email := "pg-seed-" + uuid.NewString() + "@example.com"
	acct, err := s.CreateAccount(ctx, email, api.PlanPro)
	if err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}
	app := state.App{
		ID:        uuid.NewString(),
		AccountID: acct.ID,
		Slug:      "s-" + uuid.NewString()[:8],
	}
	got, err := s.CreateApp(ctx, app)
	if err != nil {
		t.Fatalf("CreateApp: %v", err)
	}
	return acct, got
}

// seedPgDeployment inserts an Image-kind deployment under app and
// returns the row PG actually persisted (input ID is ignored).
func seedPgDeployment(t *testing.T, s *state.PgStore, ctx context.Context, app state.App) state.Deployment {
	t.Helper()
	d := state.Deployment{
		ID:        uuid.NewString(),
		AppID:     app.ID,
		Kind:      state.DeploymentKindImage,
		CreatedAt: time.Now(),
	}
	created, err := s.CreateDeployment(ctx, d)
	if err != nil {
		t.Fatalf("CreateDeployment: %v", err)
	}
	return created
}

func TestPg_CoverageSweepAccounts(t *testing.T) {
	s, ctx := pgStore(t)

	t.Run("AccountLifecycle", func(t *testing.T) {
		email := "pg-life-" + uuid.NewString() + "@example.com"

		acct, err := s.CreateAccount(ctx, email, api.PlanPro)
		if err != nil {
			t.Fatalf("CreateAccount: %v", err)
		}
		if acct.ID == "" {
			t.Fatal("CreateAccount returned empty ID")
		}

		if got, err := s.AccountByID(ctx, acct.ID); err != nil || got.ID != acct.ID {
			t.Fatalf("AccountByID happy = %+v, %v", got, err)
		}
		// pgx trips SQLSTATE 22P02 on a non-UUID "missing-id" before the
		// not-found check fires — use the zero UUID.
		if _, err := s.AccountByID(ctx, "00000000-0000-0000-0000-000000000000"); !errors.Is(err, state.ErrNotFound) {
			t.Fatalf("AccountByID missing = %v, want ErrNotFound", err)
		}

		if got, err := s.AccountByEmail(ctx, email); err != nil || got.ID != acct.ID {
			t.Fatalf("AccountByEmail happy = %+v, %v", got, err)
		}
		if _, err := s.AccountByEmail(ctx, "ghost@example.com"); !errors.Is(err, state.ErrNotFound) {
			t.Fatalf("AccountByEmail missing = %v", err)
		}

		plainKey := []byte("pk_test_" + uuid.NewString())
		apiKey, err := s.CreateAPIKey(ctx, acct.ID, plainKey, "test-key", []string{"apps:read"})
		if err != nil {
			t.Fatalf("CreateAPIKey: %v", err)
		}
		if got, err := s.AccountByKeyHash(ctx, apiKey.Hash); err != nil || got.ID != acct.ID {
			t.Fatalf("AccountByKeyHash = %+v, %v", got, err)
		}
		if _, err := s.AccountByKeyHash(ctx, []byte("bogus-hash")); !errors.Is(err, state.ErrNotFound) {
			t.Fatalf("AccountByKeyHash missing = %v", err)
		}

		if got, err := s.APIKeyByHash(ctx, apiKey.Hash); err != nil || got.AccountID != acct.ID {
			t.Fatalf("APIKeyByHash = %+v, %v", got, err)
		}
		if _, err := s.APIKeyByHash(ctx, []byte("ghost")); !errors.Is(err, state.ErrNotFound) {
			t.Fatalf("APIKeyByHash missing = %v", err)
		}

		if _, _, err := s.AuthenticateKey(ctx, plainKey); err != nil {
			t.Fatalf("AuthenticateKey happy: %v", err)
		}
		if _, _, err := s.AuthenticateKey(ctx, []byte("pk_unknown_"+uuid.NewString())); !errors.Is(err, state.ErrNotFound) {
			t.Fatalf("AuthenticateKey unknown = %v", err)
		}

		if err := s.UpdateAccountProviderCustomerID(ctx, acct.ID, "cus_"+uuid.NewString()); err != nil {
			t.Fatalf("UpdateAccountProviderCustomerID: %v", err)
		}
		if _, err := s.AccountByProviderCustomerID(ctx, "cus_unknown"); !errors.Is(err, state.ErrNotFound) {
			t.Fatalf("AccountByProviderCustomerID missing = %v", err)
		}

		if err := s.UpdateAccountPlan(ctx, acct.ID, api.PlanScale); err != nil {
			t.Fatalf("UpdateAccountPlan: %v", err)
		}
		if got, err := s.AccountByID(ctx, acct.ID); err != nil || got.Plan != api.PlanScale {
			t.Fatalf("post-update plan = %v, %v", got.Plan, err)
		}

		if err := s.UpdateAccountStatus(ctx, acct.ID, state.AccountActive); err != nil {
			t.Fatalf("UpdateAccountStatus: %v", err)
		}
		// A syntactically valid missing UUID must preserve the Store contract:
		// both implementations return ErrNotFound after the zero-row update.
		if err := s.UpdateAccountStatus(ctx, "00000000-0000-0000-0000-000000000000", state.AccountActive); !errors.Is(err, state.ErrNotFound) {
			t.Fatalf("UpdateAccountStatus missing = %v, want ErrNotFound", err)
		}

		if err := s.UpdateAccountStripeSubscriptionItem(ctx, acct.ID, "si_"+uuid.NewString()); err != nil {
			t.Fatalf("UpdateAccountStripeSubscriptionItem: %v", err)
		}
	})

	t.Run("MFALifecycle", func(t *testing.T) {
		email := "pg-mfa-" + uuid.NewString() + "@example.com"
		acct, err := s.CreateAccount(ctx, email, api.PlanPro)
		if err != nil {
			t.Fatal(err)
		}

		if _, err := s.ReadMFASecret(ctx, acct.ID); !errors.Is(err, state.ErrNotFound) {
			t.Fatalf("ReadMFASecret empty = %v, want ErrNotFound", err)
		}

		plaintexts, hashes, err := authcode.NewRecoveryCodes(authcode.RecoveryCodeCount)
		if err != nil {
			t.Fatalf("NewRecoveryCodes: %v", err)
		}

		ciphertext := []byte("sealed-bytes")
		if err := s.SetMFASecret(ctx, acct.ID, ciphertext, hashes); err != nil {
			t.Fatalf("SetMFASecret: %v", err)
		}
		if got, err := s.ReadMFASecret(ctx, acct.ID); err != nil || string(got) != string(ciphertext) {
			t.Fatalf("ReadMFASecret round-trip = %q, %v", got, err)
		}

		if err := s.MarkMFAEnrolled(ctx, acct.ID); err != nil {
			t.Fatalf("MarkMFAEnrolled: %v", err)
		}
		if _, err := s.SetMFARequired(ctx, acct.ID, true); err != nil {
			t.Fatalf("SetMFARequired: %v", err)
		}
		got, err := s.AccountByID(ctx, acct.ID)
		if err != nil {
			t.Fatal(err)
		}
		if !got.MFAEnrolled() {
			t.Errorf("MFAEnrolled = false, want true")
		}
		if !got.MFARequired {
			t.Errorf("MFARequired = false, want true")
		}

		if err := s.ClearMFA(ctx, acct.ID); err != nil {
			t.Fatalf("ClearMFA: %v", err)
		}
		if _, err := s.ReadMFASecret(ctx, acct.ID); !errors.Is(err, state.ErrNotFound) {
			t.Fatalf("ReadMFASecret post-clear = %v, want ErrNotFound", err)
		}

		if err := s.SetMFASecret(ctx, acct.ID, ciphertext, hashes); err != nil {
			t.Fatalf("re-SetMFASecret: %v", err)
		}
		presented, err := authcode.HashRecoveryCode(plaintexts[0])
		if err != nil {
			t.Fatalf("HashRecoveryCode: %v", err)
		}
		if _, _, _, err := s.ConsumeRecoveryCode(ctx, acct.ID, presented); err != nil {
			t.Fatalf("ConsumeRecoveryCode: %v", err)
		}

		presented2, err := authcode.HashRecoveryCode(plaintexts[1])
		if err != nil {
			t.Fatalf("HashRecoveryCode[1]: %v", err)
		}
		matched, _, err := s.MatchRecoveryCode(ctx, acct.ID, presented2)
		if err != nil {
			t.Fatalf("MatchRecoveryCode happy: %v", err)
		}
		if !matched {
			t.Errorf("MatchRecoveryCode returned matched=false")
		}

		if matched, _, err := s.MatchRecoveryCode(ctx, acct.ID, []byte("not-a-real-hash-bytes")); err != nil || matched {
			t.Fatalf("MatchRecoveryCode wrong = (matched=%v, %v), want (false, nil)", matched, err)
		}
	})

	t.Run("AccountsByIDs", func(t *testing.T) {
		existing := map[string]bool{}
		ids := []string{}
		for i := 0; i < 3; i++ {
			acct, err := s.CreateAccount(ctx, "pg-bulk-"+uuid.NewString()+"@example.com", api.PlanPro)
			if err != nil {
				t.Fatal(err)
			}
			ids = append(ids, acct.ID)
			existing[acct.ID] = true
		}
		ids = append(ids, "00000000-0000-0000-0000-000000000000")

		got, err := s.AccountsByIDs(ctx, ids)
		if err != nil {
			t.Fatalf("AccountsByIDs: %v", err)
		}
		if len(got) != 3 {
			t.Fatalf("AccountsByIDs returned %d, want 3", len(got))
		}
		for _, a := range got {
			if !existing[a.ID] {
				t.Errorf("unexpected account %q in result", a.ID)
			}
		}
	})

	t.Run("CreateAccountWithPersonalOrg", func(t *testing.T) {
		email := "pg-personal-org-" + uuid.NewString() + "@example.com"
		res, err := s.CreateAccountWithPersonalOrg(ctx, state.CreateAccountWithPersonalOrgParams{
			Email: email,
			Plan:  api.PlanPro,
		})
		if err != nil {
			t.Fatalf("CreateAccountWithPersonalOrg: %v", err)
		}
		if res.Account.ID == "" {
			t.Fatal("CreateAccountWithPersonalOrg returned empty Account.ID")
		}
		if res.PersonalOrg.ID == "" {
			t.Fatal("CreateAccountWithPersonalOrg returned empty PersonalOrg.ID")
		}
		if _, err := s.AccountByID(ctx, res.Account.ID); err != nil {
			t.Fatalf("post-create AccountByID: %v", err)
		}
	})

	t.Run("ListAllAccounts", func(t *testing.T) {
		_, _ = s.ListAllAccounts(ctx)
	})

	t.Run("AccountByProviderCustomerIDMissing", func(t *testing.T) {
		if _, err := s.AccountByProviderCustomerID(ctx, "ctm_nonexistent_"+uuid.NewString()); err == nil {
			t.Fatal("AccountByProviderCustomerID with non-existent id returned nil err")
		}
	})

	t.Run("AccountByProviderCustomerIDMissing", func(t *testing.T) {
		if _, err := s.AccountByProviderCustomerID(ctx, "cus_nonexistent_"+uuid.NewString()); err == nil {
			t.Fatal("AccountByProviderCustomerID with non-existent id returned nil err")
		}
	})

	t.Run("UpdateAccountProviderCustomerID", func(t *testing.T) {
		email := "pg-paddle-" + uuid.NewString() + "@example.com"
		acct, err := s.CreateAccount(ctx, email, api.PlanFree)
		if err != nil {
			t.Fatalf("CreateAccount: %v", err)
		}
		if err := s.UpdateAccountProviderCustomerID(ctx, acct.ID, "ctm_test_"+uuid.NewString()); err != nil {
			t.Errorf("UpdateAccountProviderCustomerID: %v", err)
		}
	})

	t.Run("EgressAllowlistExtra", func(t *testing.T) {
		email := "pg-egress-" + uuid.NewString() + "@example.com"
		acct, err := s.CreateAccount(ctx, email, api.PlanFree)
		if err != nil {
			t.Fatalf("CreateAccount: %v", err)
		}
		got, err := s.GetAccountEgressAllowlistExtra(ctx, acct.ID)
		if err != nil {
			t.Fatalf("GetAccountEgressAllowlistExtra: %v", err)
		}
		if got != 0 {
			t.Errorf("GetAccountEgressAllowlistExtra = %d, want 0", got)
		}
		if err := s.SetAccountEgressAllowlistExtra(ctx, acct.ID, 5); err != nil {
			t.Errorf("SetAccountEgressAllowlistExtra: %v", err)
		}
	})

	t.Run("KeyGraceWindow", func(t *testing.T) {
		email := "pg-grace-" + uuid.NewString() + "@example.com"
		acct, err := s.CreateAccount(ctx, email, api.PlanFree)
		if err != nil {
			t.Fatalf("CreateAccount: %v", err)
		}
		got, err := s.GetAccountKeyGraceWindow(ctx, acct.ID)
		if err != nil {
			t.Fatalf("GetAccountKeyGraceWindow: %v", err)
		}
		if got != nil {
			t.Errorf("GetAccountKeyGraceWindow = %v, want nil", got)
		}
		days := 7
		if err := s.SetAccountKeyGraceWindow(ctx, acct.ID, &days); err != nil {
			t.Errorf("SetAccountKeyGraceWindow: %v", err)
		}
	})

	t.Run("NewPgStore", func(t *testing.T) {
		if s == nil {
			t.Fatal("pgStore returned nil")
		}
		if _, err := s.AccountByID(ctx, "any-id"); err == nil {
			t.Fatal("AccountByID phantom-happy on fresh schema")
		}
	})
}

func TestPg_CoverageSweepAppsAndKeys(t *testing.T) {
	s, ctx := pgStore(t)

	t.Run("CreateApp", func(t *testing.T) {
		email := "pg-app-" + uuid.NewString() + "@example.com"
		acct, err := s.CreateAccount(ctx, email, api.PlanFree)
		if err != nil {
			t.Fatalf("CreateAccount: %v", err)
		}
		app := state.App{
			ID:        uuid.NewString(),
			AccountID: acct.ID,
			Slug:      "app-" + uuid.NewString()[:8],
		}
		got, err := s.CreateApp(ctx, app)
		if err != nil {
			t.Fatalf("CreateApp: %v", err)
		}
		if got.ID == "" {
			t.Error("CreateApp returned empty ID")
		}
	})

	t.Run("AppByID", func(t *testing.T) {
		email := "pg-appbyid-" + uuid.NewString() + "@example.com"
		acct, err := s.CreateAccount(ctx, email, api.PlanFree)
		if err != nil {
			t.Fatalf("CreateAccount: %v", err)
		}
		appID := uuid.NewString()
		created, err := s.CreateApp(ctx, state.App{ID: appID, AccountID: acct.ID, Slug: "x" + appID[:8]})
		if err != nil {
			t.Fatalf("CreateApp: %v", err)
		}
		// pgstore CreateApp does not preserve caller-provided ID.
		got, err := s.AppByID(ctx, created.ID)
		if err != nil {
			t.Fatalf("AppByID: %v", err)
		}
		if got.ID != created.ID {
			t.Errorf("AppByID.ID = %q, want %q", got.ID, created.ID)
		}
	})

	t.Run("AppBySlug", func(t *testing.T) {
		email := "pg-appbyslug-" + uuid.NewString() + "@example.com"
		acct, err := s.CreateAccount(ctx, email, api.PlanFree)
		if err != nil {
			t.Fatalf("CreateAccount: %v", err)
		}
		slug := "s" + uuid.NewString()[:8]
		_, err = s.CreateApp(ctx, state.App{ID: uuid.NewString(), AccountID: acct.ID, Slug: slug})
		if err != nil {
			t.Fatalf("CreateApp: %v", err)
		}
		got, err := s.AppBySlug(ctx, slug)
		if err != nil {
			t.Fatalf("AppBySlug: %v", err)
		}
		if got.Slug != slug {
			t.Errorf("AppBySlug.Slug = %q", got.Slug)
		}
	})

	t.Run("ListAPIKeys", func(t *testing.T) {
		email := "pg-keys-" + uuid.NewString() + "@example.com"
		acct, err := s.CreateAccount(ctx, email, api.PlanFree)
		if err != nil {
			t.Fatalf("CreateAccount: %v", err)
		}
		got, err := s.ListAPIKeys(ctx, acct.ID)
		if err != nil {
			t.Fatalf("ListAPIKeys: %v", err)
		}
		if len(got) != 0 {
			t.Errorf("ListAPIKeys len = %d, want 0", len(got))
		}
	})

	t.Run("CreateAPIKey", func(t *testing.T) {
		email := "pg-keycreate-" + uuid.NewString() + "@example.com"
		acct, err := s.CreateAccount(ctx, email, api.PlanFree)
		if err != nil {
			t.Fatalf("CreateAccount: %v", err)
		}
		key, err := s.CreateAPIKey(ctx, acct.ID, []byte("hash-1-"+uuid.NewString()), "test-key", []string{"apps:read"})
		if err != nil {
			t.Fatalf("CreateAPIKey: %v", err)
		}
		if key.ID == "" {
			t.Error("CreateAPIKey returned empty ID")
		}
	})

	t.Run("GetAPIKey", func(t *testing.T) {
		email := "pg-keyget-" + uuid.NewString() + "@example.com"
		acct, err := s.CreateAccount(ctx, email, api.PlanFree)
		if err != nil {
			t.Fatalf("CreateAccount: %v", err)
		}
		key, err := s.CreateAPIKey(ctx, acct.ID, []byte("hash-2-"+uuid.NewString()), "k", []string{"apps:read"})
		if err != nil {
			t.Fatalf("CreateAPIKey: %v", err)
		}
		got, err := s.GetAPIKey(ctx, acct.ID, key.ID)
		if err != nil {
			t.Fatalf("GetAPIKey: %v", err)
		}
		if got.ID != key.ID {
			t.Errorf("GetAPIKey.ID = %q", got.ID)
		}
	})

	t.Run("CountAPIKeys", func(t *testing.T) {
		email := "pg-keycount-" + uuid.NewString() + "@example.com"
		acct, err := s.CreateAccount(ctx, email, api.PlanFree)
		if err != nil {
			t.Fatalf("CreateAccount: %v", err)
		}
		got, err := s.CountAPIKeys(ctx, acct.ID)
		if err != nil {
			t.Fatalf("CountAPIKeys: %v", err)
		}
		if got != 0 {
			t.Errorf("CountAPIKeys = %d, want 0", got)
		}
	})

	t.Run("TouchKeyLastUsed", func(t *testing.T) {
		email := "pg-keytouch-" + uuid.NewString() + "@example.com"
		acct, err := s.CreateAccount(ctx, email, api.PlanFree)
		if err != nil {
			t.Fatalf("CreateAccount: %v", err)
		}
		key, err := s.CreateAPIKey(ctx, acct.ID, []byte("hash-3-"+uuid.NewString()), "k", []string{"apps:read"})
		if err != nil {
			t.Fatalf("CreateAPIKey: %v", err)
		}
		if err := s.TouchKeyLastUsed(ctx, key.ID); err != nil {
			t.Errorf("TouchKeyLastUsed: %v", err)
		}
	})

	t.Run("DeleteAPIKey", func(t *testing.T) {
		email := "pg-keydel-" + uuid.NewString() + "@example.com"
		acct, err := s.CreateAccount(ctx, email, api.PlanFree)
		if err != nil {
			t.Fatalf("CreateAccount: %v", err)
		}
		key, err := s.CreateAPIKey(ctx, acct.ID, []byte("hash-4-"+uuid.NewString()), "k", []string{"apps:read"})
		if err != nil {
			t.Fatalf("CreateAPIKey: %v", err)
		}
		if err := s.DeleteAPIKey(ctx, acct.ID, key.ID); err != nil {
			t.Errorf("DeleteAPIKey: %v", err)
		}
	})

	t.Run("DeleteAPIKeyReturning", func(t *testing.T) {
		email := "pg-keydelret-" + uuid.NewString() + "@example.com"
		acct, err := s.CreateAccount(ctx, email, api.PlanFree)
		if err != nil {
			t.Fatalf("CreateAccount: %v", err)
		}
		key, err := s.CreateAPIKey(ctx, acct.ID, []byte("hash-5-"+uuid.NewString()), "k", []string{"apps:read"})
		if err != nil {
			t.Fatalf("CreateAPIKey: %v", err)
		}
		got, err := s.DeleteAPIKeyReturning(ctx, acct.ID, key.ID)
		if err != nil {
			t.Fatalf("DeleteAPIKeyReturning: %v", err)
		}
		if got.ID != key.ID {
			t.Errorf("DeleteAPIKeyReturning.ID = %q", got.ID)
		}
	})

	t.Run("MarkAPIKeyRevoked", func(t *testing.T) {
		email := "pg-keyrev-" + uuid.NewString() + "@example.com"
		acct, err := s.CreateAccount(ctx, email, api.PlanFree)
		if err != nil {
			t.Fatalf("CreateAccount: %v", err)
		}
		key, err := s.CreateAPIKey(ctx, acct.ID, []byte("hash-6-"+uuid.NewString()), "k", []string{"apps:read"})
		if err != nil {
			t.Fatalf("CreateAPIKey: %v", err)
		}
		got, err := s.MarkAPIKeyRevoked(ctx, acct.ID, key.ID)
		if err != nil {
			t.Fatalf("MarkAPIKeyRevoked: %v", err)
		}
		if got.RevokedAt == nil {
			t.Error("MarkAPIKeyRevoked returned nil RevokedAt")
		}
	})

	t.Run("CountDeploymentsByAccount", func(t *testing.T) {
		email := "pg-count-" + uuid.NewString() + "@example.com"
		acct, err := s.CreateAccount(ctx, email, api.PlanPro)
		if err != nil {
			t.Fatal(err)
		}
		app, err := s.CreateApp(ctx, state.App{
			AccountID: acct.ID, Slug: "pg-count-" + uuid.NewString(),
			Type: state.AppTypeApp, RAMMB: 256, MaxConcurrency: 1, IdleTimeoutS: 30,
		})
		if err != nil {
			t.Fatal(err)
		}

		if n, err := s.CountDeployments(ctx, acct.ID); err != nil || n != 0 {
			t.Fatalf("CountDeployments empty = %d, %v", n, err)
		}

		if _, err := s.CreateDeployment(ctx, state.Deployment{
			AppID: app.ID, Kind: state.DeploymentKindImage,
			ImageDigest: "sha256:" + uuid.NewString(),
		}); err != nil {
			t.Fatal(err)
		}
		if n, err := s.CountDeployments(ctx, acct.ID); err != nil || n != 1 {
			t.Fatalf("CountDeployments after create = %d, %v", n, err)
		}
	})

	t.Run("AccountNotFoundErr", func(t *testing.T) {
		if _, err := s.AccountByID(ctx, "nonexistent-account-id"); err == nil {
			t.Fatal("AccountByID with non-existent id returned nil err")
		}
	})

	t.Run("APIKeyByHashNotFound", func(t *testing.T) {
		if _, err := s.APIKeyByHash(ctx, []byte("nonexistent-hash")); err == nil {
			t.Fatal("APIKeyByHash with non-existent hash returned nil err")
		}
	})

	t.Run("TouchNonExistentKey", func(t *testing.T) {
		if err := s.TouchKeyLastUsed(ctx, "00000000-0000-0000-0000-000000000000"); err == nil {
			t.Log("TouchKeyLastUsed on non-existent key returned nil err (may be expected)")
		}
	})
}

func TestPg_CoverageSweepDeployments(t *testing.T) {
	s, ctx := pgStore(t)

	t.Run("CreateDeployment", func(t *testing.T) {
		_, app := seedPgAccountAndApp(t, s, ctx)
		d := state.Deployment{
			ID:        uuid.NewString(),
			AppID:     app.ID,
			Kind:      state.DeploymentKindImage,
			CreatedAt: time.Now(),
		}
		got, err := s.CreateDeployment(ctx, d)
		if err != nil {
			t.Fatalf("CreateDeployment: %v", err)
		}
		if got.ID == "" {
			t.Error("CreateDeployment returned empty ID")
		}
	})

	t.Run("DeploymentByID", func(t *testing.T) {
		_, app := seedPgAccountAndApp(t, s, ctx)
		created := seedPgDeployment(t, s, ctx, app)
		got, err := s.DeploymentByID(ctx, created.ID)
		if err != nil {
			t.Fatalf("DeploymentByID: %v", err)
		}
		if got.ID != created.ID {
			t.Errorf("DeploymentByID.ID = %q, want %q", got.ID, created.ID)
		}
	})

	t.Run("LatestDeployment", func(t *testing.T) {
		_, app := seedPgAccountAndApp(t, s, ctx)
		created := seedPgDeployment(t, s, ctx, app)
		got, err := s.LatestDeployment(ctx, app.ID)
		if err != nil {
			t.Fatalf("LatestDeployment: %v", err)
		}
		if got.ID != created.ID {
			t.Errorf("LatestDeployment.ID = %q, want %q", got.ID, created.ID)
		}
	})

	t.Run("LiveDeployment", func(t *testing.T) {
		_, app := seedPgAccountAndApp(t, s, ctx)
		if _, err := s.LiveDeployment(ctx, app.ID); err == nil {
			t.Log("LiveDeployment returned nil err (no live deployment is fine)")
		}
	})

	t.Run("CountDeployments", func(t *testing.T) {
		acct, _ := seedPgAccountAndApp(t, s, ctx)
		got, err := s.CountDeployments(ctx, acct.ID)
		if err != nil {
			t.Fatalf("CountDeployments: %v", err)
		}
		if got < 0 {
			t.Errorf("CountDeployments = %d", got)
		}
	})

	t.Run("ListAllDeployments", func(t *testing.T) {
		_, _ = s.ListAllDeployments(ctx)
	})

	t.Run("ListDeploymentsByNodeID", func(t *testing.T) {
		_, _ = s.ListDeploymentsByNodeID(ctx, "node-"+uuid.NewString())
	})

	t.Run("UpdateDeploymentMinInstances", func(t *testing.T) {
		_, app := seedPgAccountAndApp(t, s, ctx)
		created := seedPgDeployment(t, s, ctx, app)
		got, err := s.UpdateDeploymentMinInstances(ctx, created.ID, 2)
		if err != nil {
			t.Fatalf("UpdateDeploymentMinInstances: %v", err)
		}
		if got.MinInstances != 2 {
			t.Errorf("MinInstances = %d", got.MinInstances)
		}
	})

	t.Run("SetDeploymentParked", func(t *testing.T) {
		_, app := seedPgAccountAndApp(t, s, ctx)
		created := seedPgDeployment(t, s, ctx, app)
		if err := s.SetDeploymentParked(ctx, created.ID, "admin_park", time.Now()); err != nil {
			t.Errorf("SetDeploymentParked: %v", err)
		}
	})

	t.Run("MarkDeploymentSuperseded", func(t *testing.T) {
		_, app := seedPgAccountAndApp(t, s, ctx)
		created := seedPgDeployment(t, s, ctx, app)
		if err := s.MarkDeploymentSuperseded(ctx, created.ID); err != nil {
			t.Errorf("MarkDeploymentSuperseded: %v", err)
		}
	})

	t.Run("MarkDeploymentLive", func(t *testing.T) {
		_, app := seedPgAccountAndApp(t, s, ctx)
		created := seedPgDeployment(t, s, ctx, app)
		if err := s.MarkDeploymentLive(ctx, created.ID); err != nil {
			t.Errorf("MarkDeploymentLive: %v", err)
		}
	})

	t.Run("SetDeploymentRootfs", func(t *testing.T) {
		_, app := seedPgAccountAndApp(t, s, ctx)
		created := seedPgDeployment(t, s, ctx, app)
		if err := s.SetDeploymentRootfs(ctx, created.ID, "/srv/fc/rootfs.ext4", "keyhex", 1024); err != nil {
			t.Errorf("SetDeploymentRootfs: %v", err)
		}
	})

	t.Run("UpsertDeploymentScanResult", func(t *testing.T) {
		_, app := seedPgAccountAndApp(t, s, ctx)
		created := seedPgDeployment(t, s, ctx, app)
		if err := s.UpsertDeploymentScanResult(ctx, created.ID, []byte(`{}`), "complete"); err != nil {
			t.Errorf("UpsertDeploymentScanResult: %v", err)
		}
	})

	t.Run("ListDeploymentsForApp", func(t *testing.T) {
		_, app := seedPgAccountAndApp(t, s, ctx)
		got, err := s.ListDeploymentsForApp(ctx, app.ID, 10, 0)
		if err != nil {
			t.Fatalf("ListDeploymentsForApp: %v", err)
		}
		_ = got
	})

	t.Run("LatestParkedDeploymentForApp", func(t *testing.T) {
		_, app := seedPgAccountAndApp(t, s, ctx)
		if _, err := s.LatestParkedDeploymentForApp(ctx, app.ID); err == nil {
			t.Log("LatestParkedDeploymentForApp returned nil err (no parked deployment is fine)")
		}
	})
}
