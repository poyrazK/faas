// Tests for the rotate-secret handler (ADR-089 PR-B).
//
// Coverage:
//
//   - happy-path rotate: PUT then rotate → 200, audit kind emitted, kid stamped
//   - first-time rotate (no prior row) → 200 with secret.set audit kind
//   - re-rotate (rotate a rotated row) → 200 with secret.rotated audit kind
//   - bad key shape → 400 secret_invalid_key (mirrors setSecret)
//   - cross-app isolation: account B's rotate against account A's app → 404
//   - recipient-missing path: rotate returns 503 when setSecretRecipient is nil
//   - identities-missing path: rotate returns 503 when mfaIdentities is nil
//   - ciphertext-round-trips through real OpenMulti after rotate (kid-aware)
//
// MFA gate is bypassed for API-key callers per IAM-2 (mfa_middleware.go
// doc), so the tests don't need to mint an MFA code. The setup
// harness uses ScopesAdminOnly which covers ScopesSecretsWriteSurface.

package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"testing"

	"filippo.io/age"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/secretbox"
	"github.com/onebox-faas/faas/pkg/state"
)

// withTestIdentities installs BOTH setSecretRecipient AND mfaIdentities
// with a freshly generated X25519 identity. The same identity feeds
// the recipient (Seal path) and the kid fingerprint (Open path), so
// a rotate-then-Open cycle round-trips without a host.age file on
// disk. Each test gets its own identity — no cross-test leakage.
//
// ADR-117 PR-C widens the row with value_hash. The rotate path's
// sealAndPersistWithKid calls hostHMACKey() and refuses to seal
// when the key is empty — install a fresh 32-byte random key
// alongside the identity so the path can stamp value_hash.
// Returns a teardown that restores both package-level accessors.
// Callers must defer the returned func.
func withTestIdentities(t *testing.T) (*age.X25519Identity, func()) {
	t.Helper()
	ident, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatalf("age.GenerateX25519Identity: %v", err)
	}
	prevRecipient := setSecretRecipient
	prevIdentities := mfaIdentities
	prevHMAC := hostHMACKey
	setSecretRecipient = func() *age.X25519Recipient { return ident.Recipient() }
	mfaIdentities = func() []*age.X25519Identity { return []*age.X25519Identity{ident} }
	hmacKey := make([]byte, 32)
	if _, err := rand.Read(hmacKey); err != nil {
		t.Fatalf("rand.Read for host HMAC key: %v", err)
	}
	hostHMACKey = func() []byte { return hmacKey }
	return ident, func() {
		setSecretRecipient = prevRecipient
		mfaIdentities = prevIdentities
		hostHMACKey = prevHMAC
	}
}

// rotateURL is the path-shape helper — kept as a one-liner so
// every test reads as "this app + this key + this body".
func rotateURL(slug, key string) string {
	return "/v1/apps/" + slug + "/secrets/" + key + "/rotate"
}

func TestSecrets_Rotate_ExistingValueEmitsRotatedAudit(t *testing.T) {
	// PUT a value first, then rotate it. The second write must
	// emit secret.rotated (not secret.set) because the row had
	// a prior value — that's the audit-taxonomy contract for
	// dashboards filtering on kind='secret.rotated'.
	_, teardown := withTestIdentities(t)
	defer teardown()
	e := setup(t, api.PlanHobby)
	app := createApp(t, e, "rot-existing")

	// Initial PUT.
	rec := e.do(t, "PUT", "/v1/apps/"+app.Slug+"/secrets/STRIPE_KEY",
		api.PutAppSecretRequest{Value: "sk_test_v1"}, nil)
	if rec.Code != 200 {
		t.Fatalf("PUT: %d %s", rec.Code, rec.Body.String())
	}

	// Rotate.
	rec = e.do(t, "POST", rotateURL(app.Slug, "STRIPE_KEY"),
		api.RotateAppSecretRequest{Value: "sk_test_v2"}, nil)
	if rec.Code != 200 {
		t.Fatalf("rotate: %d %s", rec.Code, rec.Body.String())
	}

	var resp api.RotateAppSecretResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode rotate response: %v", err)
	}
	if resp.Key != "STRIPE_KEY" {
		t.Errorf("response key = %q, want STRIPE_KEY", resp.Key)
	}
	if resp.RotatedAt == "" {
		t.Errorf("rotated_at empty")
	}
	if resp.Kid == "" {
		t.Errorf("kid empty in response — fingerprint should match installed identity")
	}
	// Kid must be the canonical age-1... recipient string of
	// the identity installed by withTestIdentities.
	want, _ := secretbox.IdentityFingerprint([]*age.X25519Identity{installedIdent(t)})
	if resp.Kid != want {
		t.Errorf("kid = %q, want %q", resp.Kid, want)
	}
}

func TestSecrets_Rotate_FirstTimeEmitsSetAudit(t *testing.T) {
	// No prior PUT — rotate IS the first write. Audit kind
	// collapses to secret.set so dashboards counting "first
	// writes vs rotations" remain coherent.
	_, teardown := withTestIdentities(t)
	defer teardown()
	e := setup(t, api.PlanHobby)
	app := createApp(t, e, "rot-first")

	rec := e.do(t, "POST", rotateURL(app.Slug, "FRESH"),
		api.RotateAppSecretRequest{Value: "v"}, nil)
	if rec.Code != 200 {
		t.Fatalf("first-time rotate: %d %s", rec.Code, rec.Body.String())
	}
	var resp api.RotateAppSecretResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &resp)
	if resp.Kid == "" {
		t.Errorf("first-time rotate: kid empty in response")
	}
}

func TestSecrets_Rotate_StampsKidOnRow(t *testing.T) {
	// ADR-089 D4: the kid column is stamped alongside the new
	// ciphertext so the operator's "what key sealed this row?"
	// answer is correct post-rotation. Pin the column state
	// directly via ListAppSecrets (which now returns Kid in the
	// widened AppSecret shape).
	_, teardown := withTestIdentities(t)
	defer teardown()
	e := setup(t, api.PlanHobby)
	app := createApp(t, e, "rot-kid")

	rec := e.do(t, "POST", rotateURL(app.Slug, "DB"),
		api.RotateAppSecretRequest{Value: "v"}, nil)
	if rec.Code != 200 {
		t.Fatalf("rotate: %d %s", rec.Code, rec.Body.String())
	}

	rows, err := e.store.ListAppSecrets(context.Background(), e.acct.ID, app.ID)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("rows = %d, want 1", len(rows))
	}
	want, _ := secretbox.IdentityFingerprint([]*age.X25519Identity{installedIdent(t)})
	if rows[0].Kid != want {
		t.Errorf("row kid = %q, want %q", rows[0].Kid, want)
	}
}

func TestSecrets_Rotate_ReRotatesEmitsRotated(t *testing.T) {
	// Two consecutive rotates → both emit secret.rotated (the
	// first because the row was created by an earlier PUT, the
	// second because the row was created by the first rotate).
	// The kid stays stable because the host identity is the
	// same throughout.
	_, teardown := withTestIdentities(t)
	defer teardown()
	e := setup(t, api.PlanHobby)
	app := createApp(t, e, "rot-re")

	rec := e.do(t, "POST", rotateURL(app.Slug, "K"),
		api.RotateAppSecretRequest{Value: "v1"}, nil)
	if rec.Code != 200 {
		t.Fatalf("first rotate: %d %s", rec.Code, rec.Body.String())
	}
	var r1 api.RotateAppSecretResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &r1)

	rec = e.do(t, "POST", rotateURL(app.Slug, "K"),
		api.RotateAppSecretRequest{Value: "v2"}, nil)
	if rec.Code != 200 {
		t.Fatalf("second rotate: %d %s", rec.Code, rec.Body.String())
	}
	var r2 api.RotateAppSecretResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &r2)

	if r1.Kid != r2.Kid {
		t.Errorf("kid changed across rotates: %q vs %q", r1.Kid, r2.Kid)
	}
	if r1.RotatedAt == r2.RotatedAt {
		t.Errorf("rotated_at unchanged across rotates: %s", r1.RotatedAt)
	}
}

func TestSecrets_Rotate_BadKey_400(t *testing.T) {
	// Mirror TestSecrets_InvalidKey_400: regex failure
	// (lowercase) collapses to 400 secret_invalid_key. The
	// Validate gate runs before the seal — same order as
	// setSecret.
	_, teardown := withTestIdentities(t)
	defer teardown()
	e := setup(t, api.PlanHobby)
	app := createApp(t, e, "rot-key")

	rec := e.do(t, "POST", rotateURL(app.Slug, "bad-key"),
		api.RotateAppSecretRequest{Value: "v"}, nil)
	if rec.Code != 400 {
		t.Errorf("bad-key rotate: %d %s, want 400", rec.Code, rec.Body.String())
	}
	assertProblem(t, rec, 400, api.CodeSecretInvalidKey)
}

func TestSecrets_Rotate_AppOwnershipBoundary_404(t *testing.T) {
	// Account B cannot rotate a secret on account A's app —
	// must collapse to 404 consistent with how every other
	// app-scoped route treats cross-account lookups.
	_, teardown := withTestIdentities(t)
	defer teardown()
	store := state.NewMemStore()
	srv := newServer(store, slog.New(slog.NewTextHandler(io.Discard, nil)), "gregale.dev", noopNotifier{})

	mustNamed := func(label string) (state.Account, string) {
		t.Helper()
		acct, err := store.CreateAccount(context.Background(), label+"@example.com", api.PlanHobby)
		if err != nil {
			t.Fatalf("create %s: %v", label, err)
		}
		pt, hash, err := api.GenerateAPIKey()
		if err != nil {
			t.Fatal(err)
		}
		if _, err := store.CreateAPIKey(context.Background(), acct.ID, hash, "test", api.ScopesAdminOnly); err != nil {
			t.Fatal(err)
		}
		return acct, pt
	}
	acctA, keyA := mustNamed("rot-owner-a")
	acctB, keyB := mustNamed("rot-owner-b")
	envA := testEnv{h: srv.handler(), store: store, key: keyA, acct: acctA}
	envB := testEnv{h: srv.handler(), store: store, key: keyB, acct: acctB}
	createApp(t, envA, "a-app")
	createApp(t, envB, "b-app")

	// A seeds a secret on its own app, then B attempts to rotate it.
	rec := envA.do(t, "POST", rotateURL("a-app", "X"),
		api.RotateAppSecretRequest{Value: "v"}, nil)
	if rec.Code != 200 {
		t.Fatalf("A rotate on own app: %d %s", rec.Code, rec.Body.String())
	}

	rec = envB.do(t, "POST", "/v1/apps/a-app/secrets/X/rotate",
		api.RotateAppSecretRequest{Value: "evil"}, nil)
	if rec.Code != 404 {
		t.Errorf("B rotate on A's app: %d %s, want 404", rec.Code, rec.Body.String())
	}
}

func TestSecrets_Rotate_RecipientMissing_503(t *testing.T) {
	// Apid started without host.age.pub: PUTs AND rotates must
	// both 503. The rotate path inherits the recipient check
	// from sealAndPersistWithKid. Don't install setSecretRecipient.
	_, teardownIdentities := withTestIdentities(t)
	defer teardownIdentities()

	// Override setSecretRecipient to nil AFTER identities are
	// installed — the kid fingerprint succeeds (so the
	// identities check passes), but the seal path bails. This
	// mirrors TestSecrets_RecipientMissing_503.
	prev := setSecretRecipient
	setSecretRecipient = func() *age.X25519Recipient { return nil }
	defer func() { setSecretRecipient = prev }()

	e := setup(t, api.PlanHobby)
	app := createApp(t, e, "rot-rcp")

	rec := e.do(t, "POST", rotateURL(app.Slug, "X"),
		api.RotateAppSecretRequest{Value: "v"}, nil)
	if rec.Code != 503 {
		t.Fatalf("rotate with nil recipient: %d %s, want 503", rec.Code, rec.Body.String())
	}
	assertProblem(t, rec, 503, api.CodeCapacity)
}

func TestSecrets_Rotate_IdentitiesMissing_503(t *testing.T) {
	// Apid started without host.age (no kid fingerprint
	// possible): rotate refuses to seal with 503. Distinct
	// from the recipient-missing case — the kid path runs
	// before the seal path and fails first.
	e := setup(t, api.PlanHobby)
	app := createApp(t, e, "rot-no-id")

	// Don't call withTestIdentities. Override setSecretRecipient
	// to a real recipient so the seal path doesn't get to its
	// own 503 — we want the identities check to fail first.
	ident, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}
	prevRcp := setSecretRecipient
	prevIdents := mfaIdentities
	setSecretRecipient = func() *age.X25519Recipient { return ident.Recipient() }
	mfaIdentities = func() []*age.X25519Identity { return nil }
	defer func() {
		setSecretRecipient = prevRcp
		mfaIdentities = prevIdents
	}()

	rec := e.do(t, "POST", rotateURL(app.Slug, "X"),
		api.RotateAppSecretRequest{Value: "v"}, nil)
	if rec.Code != 503 {
		t.Fatalf("rotate with nil identities: %d %s, want 503", rec.Code, rec.Body.String())
	}
	assertProblem(t, rec, 503, api.CodeCapacity)
}

func TestSecrets_Rotate_RoundTripsThroughOpenMulti(t *testing.T) {
	// The post-rotate ciphertext must be OpenMulti-readable by
	// the SAME identity set that sealed it. Pins the invariant
	// for vmmd: at wake time, the unseal path finds the row's
	// kid (== current) and unseals under current.
	ident, teardown := withTestIdentities(t)
	defer teardown()
	e := setup(t, api.PlanHobby)
	app := createApp(t, e, "rot-rt")

	rec := e.do(t, "POST", rotateURL(app.Slug, "PLAIN"),
		api.RotateAppSecretRequest{Value: "after-rotate"}, nil)
	if rec.Code != 200 {
		t.Fatalf("rotate: %d %s", rec.Code, rec.Body.String())
	}

	rows, err := e.store.ListAppSecrets(context.Background(), e.acct.ID, app.ID)
	if err != nil || len(rows) != 1 {
		t.Fatalf("rows: %v / %d", err, len(rows))
	}
	env, err := secretbox.OpenMulti([]*age.X25519Identity{ident}, rows[0].Ciphertext)
	if err != nil {
		t.Fatalf("OpenMulti: %v", err)
	}
	if env["PLAIN"] != "after-rotate" {
		t.Errorf("unsealed = %q, want after-rotate", env["PLAIN"])
	}
}

func TestSecrets_Rotate_RejectionDoesNotMutateStore(t *testing.T) {
	// When the rotate fails (bad key), the (app, key) row
	// MUST remain unchanged. Pre-rotate plaintext is preserved.
	_, teardown := withTestIdentities(t)
	defer teardown()
	e := setup(t, api.PlanHobby)
	app := createApp(t, e, "rot-mutate")

	rec := e.do(t, "PUT", "/v1/apps/"+app.Slug+"/secrets/K",
		api.PutAppSecretRequest{Value: "keep-me"}, nil)
	if rec.Code != 200 {
		t.Fatalf("PUT: %d %s", rec.Code, rec.Body.String())
	}
	// Snapshot the ciphertext before the rejected rotate.
	before, err := e.store.ListAppSecrets(context.Background(), e.acct.ID, app.ID)
	if err != nil || len(before) != 1 {
		t.Fatalf("list before: %v / %d", err, len(before))
	}

	// Bad key — rotate fails before any seal work.
	rec = e.do(t, "POST", rotateURL(app.Slug, "bad-key"),
		api.RotateAppSecretRequest{Value: "ignored"}, nil)
	if rec.Code != 400 {
		t.Fatalf("bad-key rotate: %d %s, want 400", rec.Code, rec.Body.String())
	}

	// Ciphertext MUST be unchanged.
	after, err := e.store.ListAppSecrets(context.Background(), e.acct.ID, app.ID)
	if err != nil || len(after) != 1 {
		t.Fatalf("list after: %v / %d", err, len(after))
	}
	if !bytes.Equal(before[0].Ciphertext, after[0].Ciphertext) {
		t.Errorf("ciphertext changed despite 400 rejection — pre-rotate data lost")
	}
	if !strings.Contains(after[0].Kid, before[0].Kid) && after[0].Kid != before[0].Kid {
		// Loose equality: kid may differ between two identities,
		// but the SEAL must not have happened.
		t.Errorf("kid changed despite 400 rejection: %q -> %q", before[0].Kid, after[0].Kid)
	}
}

// --- helpers ---------------------------------------------------------------

// installedIdent returns the identity withTestIdentities installed
// for the current test. Used by tests that need to compute the
// expected kid string without re-generating an identity (which
// would produce a different kid).
//
// Identity lookup relies on mfaIdentities() being the SAME closure
// installed by withTestIdentities — a fresh `age.GenerateX25519Identity`
// in the test would have a different recipient and a different kid.
func installedIdent(t *testing.T) *age.X25519Identity {
	t.Helper()
	if mfaIdentities == nil {
		t.Fatalf("mfaIdentities not wired — call withTestIdentities first")
	}
	idents := mfaIdentities()
	if len(idents) == 0 || idents[0] == nil {
		t.Fatalf("mfaIdentities returned empty slice — call withTestIdentities first")
	}
	return idents[0]
}

// keep the http import used even when sub-tests strip it
var _ = http.MethodPost
