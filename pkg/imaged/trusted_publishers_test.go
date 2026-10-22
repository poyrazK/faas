// adr: 058 — imaged verifies signed images against app_trusted_signers.

package imaged

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/cosign"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/statetest"
)

// signerStore overrides the trusted-signer read so tests can return rows
// apid's validation would refuse, or a database error.
type signerStore struct {
	Store state.Store
	rows  []state.AppTrustedSigner
	err   error
}

func (s signerStore) ListAppTrustedSignersForApp(context.Context, string) ([]state.AppTrustedSigner, error) {
	return s.rows, s.err
}

func trustedSignerApp(t *testing.T, store *state.MemStore) state.App {
	t.Helper()
	ctx := context.Background()
	account, err := store.CreateAccount(ctx, "signer@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: account.ID, Slug: "signed-app", Status: state.AppActive, RequireSigned: true})
	if err != nil {
		t.Fatal(err)
	}
	return app
}

func seedTestTrustedPublisher(t *testing.T, store *state.MemStore, app state.App, name string, key *ecdsa.PrivateKey) {
	t.Helper()
	der, err := x509.MarshalPKIXPublicKey(&key.PublicKey)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.UpsertAppTrustedSigner(t.Context(), app.AccountID, app.ID, name, der, app.AccountID); err != nil {
		t.Fatal(err)
	}
}

func publisherDER(t *testing.T) (*ecdsa.PrivateKey, []byte) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.MarshalPKIXPublicKey(&key.PublicKey)
	if err != nil {
		t.Fatal(err)
	}
	return key, der
}

// Hunt #9 H9-51: on production-us apid (control plane) wrote the trusted
// publisher to app_trusted_signers, but imaged (compute nodes) read a PEM
// directory nothing populated, so every require_signed deploy failed with
// "no trusted publishers configured". A signer apid has stored must be the
// trust set of the next verify, with no restart or notification.
func TestTrustedPublishersReadFromStore(t *testing.T) {
	store := state.NewMemStore()
	app := trustedSignerApp(t, store)
	key, der := publisherDER(t)
	if _, _, err := store.UpsertAppTrustedSigner(context.Background(), app.AccountID, app.ID, "ci-release", der, app.AccountID); err != nil {
		t.Fatal(err)
	}
	h := &Handler{store: store, log: slog.New(slog.NewTextHandler(io.Discard, nil))}

	pubs, err := h.trustedPublishers(context.Background(), app.ID)
	if err != nil {
		t.Fatalf("trustedPublishers: %v", err)
	}
	if len(pubs) != 1 || pubs[0].Name != "ci-release" || !pubs[0].PublicKey.Equal(&key.PublicKey) {
		t.Fatalf("trusted publishers = %+v, want ci-release with the stored key", pubs)
	}
}

func TestTrustedPublishersUnavailable(t *testing.T) {
	_, der := publisherDER(t)
	for _, tc := range []struct {
		name  string
		store state.Store
	}{
		{name: "no store"},
		{name: "database error", store: statetest.NewStore(signerStore{err: errors.New("connection reset")})},
		{name: "corrupt key fails closed", store: statetest.NewStore(signerStore{rows: []state.AppTrustedSigner{
			{SignerName: "good", CosignPublicKey: der},
			{SignerName: "bad", CosignPublicKey: []byte("not a key")},
		}})},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := &Handler{store: tc.store, log: slog.New(slog.NewTextHandler(io.Discard, nil))}
			_, err := h.checkImageSignature(context.Background(), state.App{ID: "app-1"}, "registry.example.com/app@sha256:abc")
			if !errors.Is(err, errTrustedPublishersUnavailable) {
				t.Fatalf("checkImageSignature error = %v, want errTrustedPublishersUnavailable", err)
			}
			if errors.Is(err, cosign.ErrSignatureInvalid) || errors.Is(err, cosign.ErrSignatureMissing) {
				t.Fatalf("an unreadable trust list was reported as an image verdict: %v", err)
			}
		})
	}
}

// A trust list imaged cannot read says nothing about the live image, so the
// revalidation sweep must not quarantine it. A store with no signers is a
// real revocation and is covered by
// TestReconcileSecuritySignaturesQuarantinesRevokedSigner.
func TestReconcileSecuritySignaturesSkipsUnreadableTrustList(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	app := trustedSignerApp(t, store)
	dep, err := store.CreateDeployment(ctx, state.Deployment{
		AppID: app.ID, Kind: state.DeploymentKindImage,
		ImageDigest: "registry.example.com/app@sha256:live", Status: state.DeployLive,
	})
	if err != nil {
		t.Fatal(err)
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	loop := &Loop{
		store:   store,
		handler: &Handler{store: statetest.NewStore(signerStore{Store: store, err: errors.New("connection reset")}), notif: &fakeNotifier{}, log: logger},
		log:     logger,
	}
	loop.reconcileSecuritySignatures(ctx, time.Date(2026, 10, 11, 18, 0, 0, 0, time.UTC))

	gotDep, err := store.DeploymentByID(ctx, dep.ID)
	if err != nil {
		t.Fatal(err)
	}
	if gotDep.ParkedReason != "" {
		t.Fatalf("parked reason = %q: a database error quarantined a live image", gotDep.ParkedReason)
	}
	gotApp, err := store.AppByID(ctx, app.ID)
	if err != nil {
		t.Fatal(err)
	}
	if gotApp.Status != state.AppActive {
		t.Fatalf("app status = %q, want active", gotApp.Status)
	}
}
