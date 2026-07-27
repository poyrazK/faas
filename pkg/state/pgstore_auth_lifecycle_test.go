package state_test

// PgStore coverage gap tests for the account-scoped auth surface.
//
// This file covers Store methods that had no PgStore test before slice 6:
//
//   MFA:        ReadMFASecret, SetMFASecret, MarkMFAEnrolled,
//               ClearMFA, SetMFARequired, CountDeployments.
//   Tokens:     IssueLoginToken, ConsumeLoginToken, DeleteOldLoginTokens.
//   OAuth:      UpsertOAuthLink (round-trip + input validation),
//               OAuthLinkByProviderSubject (full round-trip; the existing
//               parity test only smokes the error path).
//   CLI codes:  IssueCliAuthCode, PeekCliAuthCode, ClaimCliAuthCode,
//               ConsumeCliAuthCode (happy + replay + pending-keep-polling).
//   Idempotency: GetIdempotent, PutIdempotent.
//
// Helpers reused from existing harness: pgStore(t), seedLiveDeploy(t,s,ctx),
// pgTestEmail(t), createAccount(t,s,ctx,email).

import (
	"bytes"
	"errors"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// --- MFA ---------------------------------------------------------------------

// TestPg_ReadMFASecret_NotEnrolledReturnsErrNotFound pins ReadMFASecret's
// two-step shape: pgx.ErrNoRows → ErrNotFound, OR row-present-but-NULL-secret
// → ErrNotFound (the latter is the case where MFA has been Cleared but
// the row still exists).
func TestPg_ReadMFASecret_NotEnrolledReturnsErrNotFound(t *testing.T) {
	s, ctx := pgStore(t)
	acctID := createAccount(t, s, ctx, pgTestEmail(t))
	if _, err := s.ReadMFASecret(ctx, acctID); !errors.Is(err, state.ErrNotFound) {
		t.Errorf("ReadMFASecret(fresh) = %v, want ErrNotFound", err)
	}
}

// TestPg_SetMFASecret_PersistsAndReadsBack exercises the happy round-trip:
// SetMFASecret writes mfa_secret_encrypted + mfa_recovery_codes_hash, and
// ReadMFASecret returns the same ciphertext. mfa_enrolled_at is left NULL
// (StampEnrolledAt is a separate call) — reading the secret back does NOT
// require enrollment, which is what lets /verify-after-bind work.
func TestPg_SetMFASecret_PersistsAndReadsBack(t *testing.T) {
	s, ctx := pgStore(t)
	acctID := createAccount(t, s, ctx, pgTestEmail(t))
	enc := []byte("sealed-blob-32-bytes-X12345678")
	hashes := [][]byte{{0x11, 0x22}, {0x33, 0x44}}
	if err := s.SetMFASecret(ctx, acctID, enc, hashes); err != nil {
		t.Fatalf("SetMFASecret: %v", err)
	}
	got, err := s.ReadMFASecret(ctx, acctID)
	if err != nil {
		t.Fatalf("ReadMFASecret: %v", err)
	}
	if !bytes.Equal(got, enc) {
		t.Errorf("ReadMFASecret = %q, want %q", got, enc)
	}
}

// TestPg_SetMFASecret_UnknownAccountReturnsErrNotFound pins the
// RowsAffected==0 → ErrNotFound branch.
func TestPg_SetMFASecret_UnknownAccountReturnsErrNotFound(t *testing.T) {
	s, ctx := pgStore(t)
	err := s.SetMFASecret(ctx, "00000000-0000-0000-0000-000000000000", []byte("x"), nil)
	if !errors.Is(err, state.ErrNotFound) {
		t.Errorf("SetMFASecret(unknown) = %v, want ErrNotFound", err)
	}
}

// TestPg_MarkMFAEnrolled_StampsEnrolledAt pins the happy path AND verifies
// that mfa_required flips back to false (which is what makes the
// requireMFA middleware's "enrolled but not required" state observable).
//
// The migration 00049 CHECK constraint
// (accounts_mfa_enrolled_shape_chk) requires
// mfa_secret_encrypted to be NOT NULL whenever
// mfa_enrolled_at is stamped — we set the secret first so the
// enrollment stamp is accepted.
func TestPg_MarkMFAEnrolled_StampsEnrolledAt(t *testing.T) {
	s, ctx := pgStore(t)
	acctID := createAccount(t, s, ctx, pgTestEmail(t))
	if err := s.SetMFASecret(ctx, acctID, []byte("cipher"), [][]byte{{0xAB}}); err != nil {
		t.Fatalf("SetMFASecret: %v", err)
	}
	if err := s.MarkMFAEnrolled(ctx, acctID); err != nil {
		t.Fatalf("MarkMFAEnrolled: %v", err)
	}
	got, err := s.AccountByID(ctx, acctID)
	if err != nil {
		t.Fatalf("AccountByID: %v", err)
	}
	if got.MFAEnrolledAt == nil {
		t.Errorf("MFAEnrolledAt = nil, want stamped")
	}
	if got.MFARequired {
		t.Errorf("MFARequired = true, want false (MarkMFAEnrolled clears it)")
	}
}

// TestPg_MarkMFAEnrolled_UnknownAccountReturnsErrNotFound pins the
// RowsAffected==0 → ErrNotFound branch.
func TestPg_MarkMFAEnrolled_UnknownAccountReturnsErrNotFound(t *testing.T) {
	s, ctx := pgStore(t)
	err := s.MarkMFAEnrolled(ctx, "00000000-0000-0000-0000-000000000000")
	if !errors.Is(err, state.ErrNotFound) {
		t.Errorf("MarkMFAEnrolled(unknown) = %v, want ErrNotFound", err)
	}
}

// TestPg_ClearMFA_NullsSecretsAndHashes pins the three-column null-out:
// secret, recovery hashes, AND enrolled_at all go to NULL together.
// mfa_required is intentionally untouched (chokepoints re-set it).
func TestPg_ClearMFA_NullsSecretsAndHashes(t *testing.T) {
	s, ctx := pgStore(t)
	acctID := createAccount(t, s, ctx, pgTestEmail(t))
	if err := s.SetMFASecret(ctx, acctID, []byte("cipher"), [][]byte{{0xAB}}); err != nil {
		t.Fatalf("SetMFASecret: %v", err)
	}
	if err := s.MarkMFAEnrolled(ctx, acctID); err != nil {
		t.Fatalf("MarkMFAEnrolled: %v", err)
	}
	if err := s.ClearMFA(ctx, acctID); err != nil {
		t.Fatalf("ClearMFA: %v", err)
	}
	if _, err := s.ReadMFASecret(ctx, acctID); !errors.Is(err, state.ErrNotFound) {
		t.Errorf("ReadMFASecret after Clear = %v, want ErrNotFound (NULL secret)", err)
	}
	got, err := s.AccountByID(ctx, acctID)
	if err != nil {
		t.Fatalf("AccountByID: %v", err)
	}
	if got.MFAEnrolledAt != nil {
		t.Errorf("MFAEnrolledAt = %v, want nil after Clear", got.MFAEnrolledAt)
	}
}

// TestPg_ClearMFA_UnknownAccountReturnsErrNotFound pins the
// RowsAffected==0 → ErrNotFound branch.
func TestPg_ClearMFA_UnknownAccountReturnsErrNotFound(t *testing.T) {
	s, ctx := pgStore(t)
	err := s.ClearMFA(ctx, "00000000-0000-0000-0000-000000000000")
	if !errors.Is(err, state.ErrNotFound) {
		t.Errorf("ClearMFA(unknown) = %v, want ErrNotFound", err)
	}
}

// TestPg_SetMFARequired_FlipsAndReportsChanged pins the changed=true path.
// After the call the row carries the new value AND the method reports the
// change (so the chokepoint can suppress a duplicate audit Emit on a
// redelivered webhook).
func TestPg_SetMFARequired_FlipsAndReportsChanged(t *testing.T) {
	s, ctx := pgStore(t)
	acctID := createAccount(t, s, ctx, pgTestEmail(t))
	changed, err := s.SetMFARequired(ctx, acctID, true)
	if err != nil {
		t.Fatalf("SetMFARequired(true): %v", err)
	}
	if !changed {
		t.Errorf("changed = false on first call, want true")
	}
	got, err := s.AccountByID(ctx, acctID)
	if err != nil {
		t.Fatalf("AccountByID: %v", err)
	}
	if !got.MFARequired {
		t.Errorf("MFARequired = false after flip, want true")
	}
}

// TestPg_SetMFARequired_NoOpReportsUnchanged pins the changed=false branch
// (the WHERE … AND mfa_required <> $2 clause filters out a no-op write).
// The handler relies on `changed=false` to skip the audit Emit on webhook
// replays — that's the load-bearing bit.
func TestPg_SetMFARequired_NoOpReportsUnchanged(t *testing.T) {
	s, ctx := pgStore(t)
	acctID := createAccount(t, s, ctx, pgTestEmail(t))
	// First flip: changes from false→true → changed=true.
	if _, err := s.SetMFARequired(ctx, acctID, true); err != nil {
		t.Fatalf("first SetMFARequired: %v", err)
	}
	// Second flip with same value: changed=false (no-op write detected).
	changed, err := s.SetMFARequired(ctx, acctID, true)
	if err != nil {
		t.Fatalf("second SetMFARequired: %v", err)
	}
	if changed {
		t.Errorf("changed = true on no-op write, want false (audit should be suppressed)")
	}
}

// TestPg_SetMFARequired_UnknownAccountReturnsErrNotFound pins the
// follow-up existence-check branch (RowsAffected==0 + EXISTS=0 → ErrNotFound).
func TestPg_SetMFARequired_UnknownAccountReturnsErrNotFound(t *testing.T) {
	s, ctx := pgStore(t)
	_, err := s.SetMFARequired(ctx, "00000000-0000-0000-0000-000000000000", true)
	if !errors.Is(err, state.ErrNotFound) {
		t.Errorf("SetMFARequired(unknown) = %v, want ErrNotFound", err)
	}
}

// TestPg_CountDeployments_ExcludesFailedAndSuperseded pins the JOIN shape:
// failed and superseded deployments are NOT counted (the chokepoint's
// 2nd-deploy gate only cares about live workload). Also pins the
// apps.status <> 'deleted' filter (tombstoned apps are invisible too).
//
// Note: CreateDeployment's INSERT hard-codes status='pending' regardless
// of the caller's Status field, AND auto-supersedes any prior
// pending|live row on the same app. To stage a failed and a superseded
// row, we (1) put them on separate apps so the seed's live row stays
// untouched, and (2) flip each one via SetDeploymentFailed /
// MarkDeploymentSuperseded — both are real Store methods that the test
// fixture is also exercising.
func TestPg_CountDeployments_ExcludesFailedAndSuperseded(t *testing.T) {
	s, ctx := pgStore(t)
	acctID, seededAppID, liveDepID := seedLiveDeploy(t, s, ctx)

	// One failed row on its own app — must NOT be counted.
	failedAppID := createApp(t, s, ctx, acctID, "failed-app")
	failedDep, err := s.CreateDeployment(ctx, state.Deployment{
		AppID: failedAppID, Kind: state.DeploymentKindImage, ImageDigest: "sha256:bad",
	})
	if err != nil {
		t.Fatalf("CreateDeployment(failed): %v", err)
	}
	if _, err := s.SetDeploymentFailed(ctx, failedDep.ID, "build_failed", "compile error"); err != nil {
		t.Fatalf("SetDeploymentFailed: %v", err)
	}

	// One superseded row on yet another app — must NOT be counted.
	supAppID := createApp(t, s, ctx, acctID, "superseded-app")
	supDep, err := s.CreateDeployment(ctx, state.Deployment{
		AppID: supAppID, Kind: state.DeploymentKindImage, ImageDigest: "sha256:sup",
	})
	if err != nil {
		t.Fatalf("CreateDeployment(superseded): %v", err)
	}
	if err := s.MarkDeploymentSuperseded(ctx, supDep.ID); err != nil {
		t.Fatalf("MarkDeploymentSuperseded: %v", err)
	}

	// One extra pending deployment on yet a third app — DOES count.
	extraAppID := createApp(t, s, ctx, acctID, "extra-app")
	if _, err := s.CreateDeployment(ctx, state.Deployment{
		AppID: extraAppID, Kind: state.DeploymentKindImage, ImageDigest: "sha256:new",
	}); err != nil {
		t.Fatalf("CreateDeployment(extra): %v", err)
	}

	_ = seededAppID
	_ = liveDepID

	n, err := s.CountDeployments(ctx, acctID)
	if err != nil {
		t.Fatalf("CountDeployments: %v", err)
	}
	if n != 2 {
		t.Errorf("CountDeployments = %d, want 2 (seeded live + extra pending; failed/superseded filtered)", n)
	}
}

// --- login tokens ------------------------------------------------------------

// TestPg_IssueLoginToken_StoresHash pins the ON CONFLICT DO NOTHING insert
// shape (the same hash can be re-issued — the raw token is single-use).
// Reading the hash back isn't a Store method; we verify via the consume
// path which is exercised below.
func TestPg_IssueLoginToken_StoresHash(t *testing.T) {
	s, ctx := pgStore(t)
	acctID, _, _ := seedLiveDeploy(t, s, ctx)
	hash := []byte("token-hash-32-bytes-X1234567890abc")
	expires := time.Now().UTC().Add(time.Hour)
	if err := s.IssueLoginToken(ctx, hash, acctID, expires); err != nil {
		t.Fatalf("IssueLoginToken: %v", err)
	}
	got, err := s.ConsumeLoginToken(ctx, hash)
	if err != nil {
		t.Fatalf("ConsumeLoginToken: %v", err)
	}
	if got != acctID {
		t.Errorf("ConsumeLoginToken = %q, want %q", got, acctID)
	}
}

// TestPg_ConsumeLoginToken_ReplayReturnsErrNotFound pins the replay branch:
// once consumed, the WHERE `consumed_at is null` filter rejects the second
// call. Returning ErrNotFound (not a stale accountID) is the wire contract
// the post-login handler depends on.
func TestPg_ConsumeLoginToken_ReplayReturnsErrNotFound(t *testing.T) {
	s, ctx := pgStore(t)
	acctID, _, _ := seedLiveDeploy(t, s, ctx)
	hash := []byte("replay-token-32-bytes-X1234567890abc")
	if err := s.IssueLoginToken(ctx, hash, acctID, time.Now().Add(time.Hour)); err != nil {
		t.Fatalf("IssueLoginToken: %v", err)
	}
	if _, err := s.ConsumeLoginToken(ctx, hash); err != nil {
		t.Fatalf("first ConsumeLoginToken: %v", err)
	}
	if _, err := s.ConsumeLoginToken(ctx, hash); !errors.Is(err, state.ErrNotFound) {
		t.Errorf("replay ConsumeLoginToken = %v, want ErrNotFound", err)
	}
}

// TestPg_ConsumeLoginToken_ExpiredReturnsErrNotFound pins the expiry branch:
// `expires_at > now()` filters out stale tokens. The store doesn't return
// a distinct sentinel — handlers can't tell expired from replay from this
// single call, which is fine for the wire contract.
func TestPg_ConsumeLoginToken_ExpiredReturnsErrNotFound(t *testing.T) {
	s, ctx := pgStore(t)
	acctID, _, _ := seedLiveDeploy(t, s, ctx)
	hash := []byte("expired-token-32-bytes-X1234567890ab")
	if err := s.IssueLoginToken(ctx, hash, acctID, time.Now().Add(-time.Minute)); err != nil {
		t.Fatalf("IssueLoginToken: %v", err)
	}
	if _, err := s.ConsumeLoginToken(ctx, hash); !errors.Is(err, state.ErrNotFound) {
		t.Errorf("expired ConsumeLoginToken = %v, want ErrNotFound", err)
	}
}

// TestPg_DeleteOldLoginTokens_PurgesExpired pins the maintenance hook:
// rows whose expires_at < before are deleted (consumed and unconsumed both).
func TestPg_DeleteOldLoginTokens_PurgesExpired(t *testing.T) {
	s, ctx := pgStore(t)
	acctID, _, _ := seedLiveDeploy(t, s, ctx)
	old := []byte("old-token-32-bytes-X1234567890abcde")
	if err := s.IssueLoginToken(ctx, old, acctID, time.Now().Add(-time.Hour)); err != nil {
		t.Fatalf("IssueLoginToken(old): %v", err)
	}
	fresh := []byte("fresh-token-32-bytes-X1234567890abcf")
	if err := s.IssueLoginToken(ctx, fresh, acctID, time.Now().Add(time.Hour)); err != nil {
		t.Fatalf("IssueLoginToken(fresh): %v", err)
	}
	deleted, err := s.DeleteOldLoginTokens(ctx, time.Now())
	if err != nil {
		t.Fatalf("DeleteOldLoginTokens: %v", err)
	}
	if deleted < 1 {
		t.Errorf("deleted = %d, want >= 1 (the expired row)", deleted)
	}
	// The fresh row must still consume cleanly.
	if _, err := s.ConsumeLoginToken(ctx, fresh); err != nil {
		t.Errorf("fresh Consume after delete: %v, want nil", err)
	}
}

// --- OAuth links -------------------------------------------------------------

// TestPg_OAuthLinkByProviderSubject_RoundTrip is the missing happy-path
// assertion (the parity smoke at pgstore_coverage_parity_test.go:111-122
// only exercises the error shape). The (provider, sub) → account_id link
// is the OAuth callback's primary lookup; calling Peek/Link side without
// the read-back side means the round-trip is unverified.
func TestPg_OAuthLinkByProviderSubject_RoundTrip(t *testing.T) {
	s, ctx := pgStore(t)
	acctID, _, _ := seedLiveDeploy(t, s, ctx)
	if err := s.UpsertOAuthLink(ctx, acctID, "google", "google-sub-abc", "u@ex.com", true); err != nil {
		t.Fatalf("UpsertOAuthLink: %v", err)
	}
	link, err := s.OAuthLinkByProviderSubject(ctx, "google", "google-sub-abc")
	if err != nil {
		t.Fatalf("OAuthLinkByProviderSubject: %v", err)
	}
	if link.AccountID != acctID {
		t.Errorf("AccountID = %q, want %q", link.AccountID, acctID)
	}
	if link.Email != "u@ex.com" {
		t.Errorf("Email = %q, want u@ex.com", link.Email)
	}
	if !link.EmailVerified {
		t.Errorf("EmailVerified = false, want true")
	}
}

// TestPg_OAuthLinkByProviderSubject_MissingReturnsErrNotFound pins the
// miss path the dashboard's "wrong provider" branch depends on. OAuthLink
// read returns ErrNotFound via the shared mapErr path (pgx.ErrNoRows).
func TestPg_OAuthLinkByProviderSubject_MissingReturnsErrNotFound(t *testing.T) {
	s, ctx := pgStore(t)
	_, err := s.OAuthLinkByProviderSubject(ctx, "github", "no-such-sub")
	if !errors.Is(err, state.ErrNotFound) {
		t.Errorf("OAuthLinkByProviderSubject(missing) = %v, want ErrNotFound", err)
	}
}

// TestPg_UpsertOAuthLink_EmptyArgsReturnErrInvalidArgument pins the input
// validation gate (account_id/provider/provider_subject required).
func TestPg_UpsertOAuthLink_EmptyArgsReturnErrInvalidArgument(t *testing.T) {
	s, ctx := pgStore(t)
	acctID, _, _ := seedLiveDeploy(t, s, ctx)
	cases := []struct {
		name string
		fn   func() error
	}{
		{"empty account", func() error { return s.UpsertOAuthLink(ctx, "", "g", "sub", "x", true) }},
		{"empty provider", func() error { return s.UpsertOAuthLink(ctx, acctID, "", "sub", "x", true) }},
		{"empty subject", func() error { return s.UpsertOAuthLink(ctx, acctID, "g", "", "x", true) }},
	}
	for _, tc := range cases {
		err := tc.fn()
		if !errors.Is(err, state.ErrInvalidArgument) {
			t.Errorf("%s = %v, want ErrInvalidArgument", tc.name, err)
		}
	}
}

// --- CLI auth codes ---------------------------------------------------------

// TestPg_PeekCliAuthCode_FreshPendingReturnsStatusPending pins the
// happy pre-claim state: a freshly-minted code is in 'pending' status with
// an empty account_id. The dashboard's GET /cli-auth render uses this to
// show the email-input form.
func TestPg_PeekCliAuthCode_FreshPendingReturnsStatusPending(t *testing.T) {
	s, ctx := pgStore(t)
	hash := []byte("cli-pending-32-bytes-X1234567890abc")
	if err := s.IssueCliAuthCode(ctx, hash, time.Now().Add(time.Hour)); err != nil {
		t.Fatalf("IssueCliAuthCode: %v", err)
	}
	status, aid, err := s.PeekCliAuthCode(ctx, hash)
	if err != nil {
		t.Fatalf("PeekCliAuthCode: %v", err)
	}
	if status != api.CliAuthStatusPending {
		t.Errorf("status = %q, want pending", status)
	}
	if aid != "" {
		t.Errorf("accountID = %q, want empty (still pending claim)", aid)
	}
}

// TestPg_PeekCliAuthCode_MissingReturnsErrNotFound pins the not-yet-minted
// branch — the row never existed, so we get the expired sentinel + ErrNotFound.
func TestPg_PeekCliAuthCode_MissingReturnsErrNotFound(t *testing.T) {
	s, ctx := pgStore(t)
	_, _, err := s.PeekCliAuthCode(ctx, []byte("never-minted-32-bytes-X1234567890"))
	if !errors.Is(err, state.ErrNotFound) {
		t.Errorf("PeekCliAuthCode(missing) = %v, want ErrNotFound", err)
	}
}

// TestPg_PeekCliAuthCode_ExpiredReturnsErrNotFound pins the past-expiry
// branch — the WHERE `expires_at > now()` filter rejects it.
func TestPg_PeekCliAuthCode_ExpiredReturnsErrNotFound(t *testing.T) {
	s, ctx := pgStore(t)
	hash := []byte("cli-expired-32-bytes-X1234567890abc")
	if err := s.IssueCliAuthCode(ctx, hash, time.Now().Add(-time.Minute)); err != nil {
		t.Fatalf("IssueCliAuthCode: %v", err)
	}
	_, _, err := s.PeekCliAuthCode(ctx, hash)
	if !errors.Is(err, state.ErrNotFound) {
		t.Errorf("PeekCliAuthCode(expired) = %v, want ErrNotFound", err)
	}
}

// TestPg_ClaimCliAuthCode_BindsAccountID pins the happy claim: the
// pending → consumed transition lands the account_id atomically without
// touching consumed_at (that's the ConsumeCliAuthCode CAS gate).
func TestPg_ClaimCliAuthCode_BindsAccountID(t *testing.T) {
	s, ctx := pgStore(t)
	acctID, _, _ := seedLiveDeploy(t, s, ctx)
	hash := []byte("cli-claim-32-bytes-X1234567890abcdef")
	if err := s.IssueCliAuthCode(ctx, hash, time.Now().Add(time.Hour)); err != nil {
		t.Fatalf("IssueCliAuthCode: %v", err)
	}
	if err := s.ClaimCliAuthCode(ctx, hash, acctID); err != nil {
		t.Fatalf("ClaimCliAuthCode: %v", err)
	}
	// Post-claim peek: status should now be 'consumed' with the bound account.
	status, aid, err := s.PeekCliAuthCode(ctx, hash)
	if err != nil {
		t.Fatalf("PeekCliAuthCode(post-claim): %v", err)
	}
	if status != api.CliAuthStatusConsumed {
		t.Errorf("status = %q, want consumed", status)
	}
	if aid != acctID {
		t.Errorf("accountID = %q, want %q", aid, acctID)
	}
}

// TestPg_ClaimCliAuthCode_AlreadyClaimedReturnsErrConflict pins the
// "second claimer" branch: the post-classification SELECT finds the row
// exists, expires_at > now(), but status != 'pending' → ErrConflict.
func TestPg_ClaimCliAuthCode_AlreadyClaimedReturnsErrConflict(t *testing.T) {
	s, ctx := pgStore(t)
	acctID, _, _ := seedLiveDeploy(t, s, ctx)
	hash := []byte("cli-twice-32-bytes-X1234567890abcdef")
	if err := s.IssueCliAuthCode(ctx, hash, time.Now().Add(time.Hour)); err != nil {
		t.Fatalf("IssueCliAuthCode: %v", err)
	}
	if err := s.ClaimCliAuthCode(ctx, hash, acctID); err != nil {
		t.Fatalf("first ClaimCliAuthCode: %v", err)
	}
	if err := s.ClaimCliAuthCode(ctx, hash, acctID); !errors.Is(err, state.ErrConflict) {
		t.Errorf("second ClaimCliAuthCode = %v, want ErrConflict", err)
	}
}

// TestPg_ClaimCliAuthCode_ExpiredReturnsErrNotFound pins the past-expiry
// branch: the post-classification SELECT finds the row exists but
// fresh=false → ErrNotFound (not ErrConflict, even though the row existed).
func TestPg_ClaimCliAuthCode_ExpiredReturnsErrNotFound(t *testing.T) {
	s, ctx := pgStore(t)
	acctID, _, _ := seedLiveDeploy(t, s, ctx)
	hash := []byte("cli-exp-32-bytes-X1234567890abcdef")
	if err := s.IssueCliAuthCode(ctx, hash, time.Now().Add(-time.Minute)); err != nil {
		t.Fatalf("IssueCliAuthCode: %v", err)
	}
	if err := s.ClaimCliAuthCode(ctx, hash, acctID); !errors.Is(err, state.ErrNotFound) {
		t.Errorf("ClaimCliAuthCode(expired) = %v, want ErrNotFound", err)
	}
}

// TestPg_ConsumeCliAuthCode_HappyReturnsConsumedAndAccount pins the
// load-bearing missing-in-parity happy path: after a successful claim,
// the first ConsumeCliAuthCode call returns (Consumed, acct_id, nil)
// — which is the wire shape that triggers CLI API-key minting.
func TestPg_ConsumeCliAuthCode_HappyReturnsConsumedAndAccount(t *testing.T) {
	s, ctx := pgStore(t)
	acctID, _, _ := seedLiveDeploy(t, s, ctx)
	hash := []byte("cli-mint-32-bytes-X1234567890abcdef1")
	if err := s.IssueCliAuthCode(ctx, hash, time.Now().Add(time.Hour)); err != nil {
		t.Fatalf("IssueCliAuthCode: %v", err)
	}
	if err := s.ClaimCliAuthCode(ctx, hash, acctID); err != nil {
		t.Fatalf("ClaimCliAuthCode: %v", err)
	}
	status, aid, err := s.ConsumeCliAuthCode(ctx, hash)
	if err != nil {
		t.Fatalf("first ConsumeCliAuthCode: %v", err)
	}
	if status != api.CliAuthStatusConsumed {
		t.Errorf("status = %q, want consumed", status)
	}
	if aid != acctID {
		t.Errorf("accountID = %q, want %q", aid, acctID)
	}
}

// TestPg_ConsumeCliAuthCode_ReplayReturnsErrNotFound pins the F4-review
// single-use CAS: a second ConsumeCliAuthCode call sees consumed_at NOT
// null, hits the post-classification SELECT, finds the row exists but
// is no longer in the 'just CAS'd' shape → ErrNotFound. The CLI cannot
// mint a second API key with the same code.
func TestPg_ConsumeCliAuthCode_ReplayReturnsErrNotFound(t *testing.T) {
	s, ctx := pgStore(t)
	acctID, _, _ := seedLiveDeploy(t, s, ctx)
	hash := []byte("cli-replay-32-bytes-X1234567890abcd1")
	if err := s.IssueCliAuthCode(ctx, hash, time.Now().Add(time.Hour)); err != nil {
		t.Fatalf("IssueCliAuthCode: %v", err)
	}
	if err := s.ClaimCliAuthCode(ctx, hash, acctID); err != nil {
		t.Fatalf("ClaimCliAuthCode: %v", err)
	}
	if _, _, err := s.ConsumeCliAuthCode(ctx, hash); err != nil {
		t.Fatalf("first ConsumeCliAuthCode: %v", err)
	}
	if _, _, err := s.ConsumeCliAuthCode(ctx, hash); !errors.Is(err, state.ErrNotFound) {
		t.Errorf("replay ConsumeCliAuthCode = %v, want ErrNotFound", err)
	}
}

// TestPg_ConsumeCliAuthCode_PendingKeepsPolling pins the
// "dashboard hasn't claimed yet" branch: Consume sees status='pending',
// account_id IS NULL → returns (Pending, "", nil) so the CLI keeps polling
// (rather than minting a useless NULL-FK api_keys row).
func TestPg_ConsumeCliAuthCode_PendingKeepsPolling(t *testing.T) {
	s, ctx := pgStore(t)
	hash := []byte("cli-still-pending-32-bytes-X123456789")
	if err := s.IssueCliAuthCode(ctx, hash, time.Now().Add(time.Hour)); err != nil {
		t.Fatalf("IssueCliAuthCode: %v", err)
	}
	status, aid, err := s.ConsumeCliAuthCode(ctx, hash)
	if err != nil {
		t.Fatalf("ConsumeCliAuthCode(pending): %v", err)
	}
	if status != api.CliAuthStatusPending {
		t.Errorf("status = %q, want pending", status)
	}
	if aid != "" {
		t.Errorf("accountID = %q, want empty (no dashboard claim yet)", aid)
	}
}

// --- idempotency ------------------------------------------------------------

// TestPg_GetIdempotent_MissingReturnsErrNotFound pins the not-stored
// branch (24-hour TTL window also lives in the WHERE clause).
func TestPg_GetIdempotent_MissingReturnsErrNotFound(t *testing.T) {
	s, ctx := pgStore(t)
	acctID, _, _ := seedLiveDeploy(t, s, ctx)
	if _, _, err := s.GetIdempotent(ctx, acctID, "never-stored-key"); !errors.Is(err, state.ErrNotFound) {
		t.Errorf("GetIdempotent(missing) = %v, want ErrNotFound", err)
	}
}

// TestPg_PutIdempotent_ThenGetRoundTrip pins the happy path: the Put
// upsert (ON CONFLICT DO UPDATE) stores the body, the Get reads it back
// with the same status + body. The conflict-update branch is exercised
// by a second Put with new values that overwrites in place.
func TestPg_PutIdempotent_ThenGetRoundTrip(t *testing.T) {
	s, ctx := pgStore(t)
	acctID, _, _ := seedLiveDeploy(t, s, ctx)
	first := []byte("first-body")
	if err := s.PutIdempotent(ctx, acctID, "key-1", 200, first); err != nil {
		t.Fatalf("first PutIdempotent: %v", err)
	}
	status, body, err := s.GetIdempotent(ctx, acctID, "key-1")
	if err != nil {
		t.Fatalf("GetIdempotent: %v", err)
	}
	if status != 200 {
		t.Errorf("status = %d, want 200", status)
	}
	if !bytes.Equal(body, first) {
		t.Errorf("body = %q, want %q", body, first)
	}
	// Second Put overwrites — the ON CONFLICT DO UPDATE branch.
	if err := s.PutIdempotent(ctx, acctID, "key-1", 201, []byte("updated-body")); err != nil {
		t.Fatalf("second PutIdempotent: %v", err)
	}
	status, body, err = s.GetIdempotent(ctx, acctID, "key-1")
	if err != nil {
		t.Fatalf("GetIdempotent(after-overwrite): %v", err)
	}
	if status != 201 || !bytes.Equal(body, []byte("updated-body")) {
		t.Errorf("post-overwrite = (%d, %q), want (201, updated-body)", status, body)
	}
}
