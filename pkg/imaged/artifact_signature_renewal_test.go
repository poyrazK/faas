package imaged

// adr: 435

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"errors"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/cosign"
	"github.com/onebox-faas/faas/pkg/oci"
	"github.com/onebox-faas/faas/pkg/state"
)

type retainedRenewalTestPuller struct {
	*resolvingTestPuller
	t           *testing.T
	ref, digest string
	failure     error
}

func (p *retainedRenewalTestPuller) ResolveImage(context.Context, string, *oci.BasicAuth) (oci.ImageResolution, error) {
	p.t.Fatal("renewal resolved a mutable tag or another child")
	return oci.ImageResolution{}, nil
}
func (p *retainedRenewalTestPuller) PullDigest(context.Context, string) (string, error) {
	p.t.Fatal("renewal resolved an already-retained source digest")
	return "", nil
}
func (p *retainedRenewalTestPuller) PullDigestWithAuth(context.Context, string, *oci.BasicAuth) (string, error) {
	p.t.Fatal("renewal resolved an already-retained authenticated source digest")
	return "", nil
}
func (p *retainedRenewalTestPuller) PullImageSignatureAttachments(_ context.Context, ref, digest string, _ *oci.BasicAuth) ([]oci.ImageSignatureAttachment, error) {
	if ref != p.ref || digest != p.digest {
		p.t.Fatal("renewal attachment lookup changed the retained signed subject")
	}
	p.signatureRefs = append(p.signatureRefs, ref)
	return p.attachments, p.failure
}

func TestProducedScanRenewalPreservesOriginDuringRegistryOutage(t *testing.T) {
	h, th := producedScanFixture(t, true)
	p := h.oci.(*resolvingTestPuller)
	h.oci = &retainedRenewalTestPuller{resolvingTestPuller: p, t: t, ref: p.resolution.SourceReference, digest: p.resolution.SourceDigest, failure: errors.New("registry unavailable")}
	h.WithGrypeRun(func(context.Context, string) (*ScanResult, error) { return producedScanResult(t, false), nil })
	if err := th.store.UpdateDeploymentStatus(t.Context(), th.dep.ID, state.DeployLive, ""); err != nil {
		t.Fatal(err)
	}
	for _, workload := range []string{"", "metrics"} {
		before, err := th.store.GetCurrentDeploymentRegistryRootfs(t.Context(), th.app.AccountID, th.app.ID, th.dep.ID, workload)
		if err != nil {
			t.Fatal(err)
		}
		origin, err := th.store.GetDeploymentRegistryVerificationByID(t.Context(), th.app.AccountID, th.app.ID, th.dep.ID, before.Input.RegistryVerificationID)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := h.renewProducedSignature(t.Context(), th.app, th.dep, before); err != nil {
			t.Fatal(err)
		}
		old, err := th.store.GetDeploymentRegistryVerificationByID(t.Context(), th.app.AccountID, th.app.ID, th.dep.ID, origin.ID)
		if err != nil || !old.VerifiedAt.Equal(origin.VerifiedAt) || !old.ExpiresAt.Equal(origin.ExpiresAt) {
			t.Fatalf("origin clock extended: %v", err)
		}
	}
	if err := collectProducedFixtureScans(t, h, th); err != nil {
		t.Fatal(err)
	}
	for _, workload := range []string{"", "metrics"} {
		root, err := th.store.GetCurrentDeploymentRegistryRootfs(t.Context(), th.app.AccountID, th.app.ID, th.dep.ID, workload)
		scan, scanErr := th.store.GetCurrentDeploymentArtifactScan(t.Context(), th.app.AccountID, th.app.ID, th.dep.ID, workload)
		if err != nil || scanErr != nil || scan.Input.RootfsProducerID != root.ID || scan.Input.RegistryVerificationID == "" || scan.Input.RegistryVerificationID == root.Input.RegistryVerificationID {
			t.Fatalf("renewal lost original producer or new current signature: %v %v", err, scanErr)
		}
	}
}

func TestProducedSignatureRenewalUsesRotatedKeyForExactRetainedSubject(t *testing.T) {
	for _, unavailable := range []bool{false, true} {
		t.Run(map[bool]string{false: "current signature", true: "rotation unavailable"}[unavailable], func(t *testing.T) {
			h, th := producedScanFixture(t, false)
			root, err := th.store.GetCurrentDeploymentRegistryRootfs(t.Context(), th.app.AccountID, th.app.ID, th.dep.ID, "")
			if err != nil {
				t.Fatal(err)
			}
			p := h.oci.(*resolvingTestPuller)
			wrapped := &retainedRenewalTestPuller{resolvingTestPuller: p, t: t, ref: p.resolution.SourceReference, digest: p.resolution.SourceDigest}
			key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
			if err != nil {
				t.Fatal(err)
			}
			der, err := x509.MarshalPKIXPublicKey(&key.PublicKey)
			if err != nil {
				t.Fatal(err)
			}
			if _, _, err := th.store.UpsertAppTrustedSigner(t.Context(), th.app.AccountID, th.app.ID, "company", der, th.app.AccountID); err != nil {
				t.Fatal(err)
			}
			h.trustedPublishersCache[th.app.ID] = []cosign.TrustedPublisher{{Name: "company", PublicKey: &key.PublicKey}}
			sum := sha256.Sum256(p.attachments[0].Payload)
			sig, err := ecdsa.SignASN1(rand.Reader, key, sum[:])
			if err != nil {
				t.Fatal(err)
			}
			p.attachments[0].Signature = sig
			if unavailable {
				wrapped.failure = oci.ErrImageSignatureMissing
			}
			h.oci = wrapped
			before := len(p.signatureRefs)
			value, err := h.renewProducedSignature(t.Context(), th.app, th.dep, root)
			if len(p.signatureRefs) != before+1 {
				t.Fatal("rotation did not fetch the retained subject attachment")
			}
			if unavailable {
				if !errors.Is(err, cosign.ErrSignatureMissing) {
					t.Fatalf("unavailable rotated signature accepted: %v", err)
				}
				return
			}
			if err != nil || value.ID == root.Input.RegistryVerificationID || value.Input.Proof.PublisherKeySHA256 == "" {
				t.Fatalf("current approved key failed renewal: %v", err)
			}
		})
	}
}

type dueBaseRenewalStore struct {
	*state.MemStore
	due       bool
	published int
}

func (s *dueBaseRenewalStore) GetFreshBaseImageScan(ctx context.Context, id, hash string) (state.BaseImageScan, error) {
	value, err := s.MemStore.GetFreshBaseImageScan(ctx, id, hash)
	if err == nil && s.due {
		// Only the returned scheduling clock is adjusted. Immutable storage
		// evidence stays unchanged; this is not a native lease acceptance test.
		value.ScannedAt = time.Now().UTC().Add(-api.ApplicationStandardArtifactScanRenewEvery)
	}
	return value, err
}
func (s *dueBaseRenewalStore) PublishBaseImageScan(ctx context.Context, in state.BaseImageScanInput) (state.BaseImageScan, error) {
	value, err := s.MemStore.PublishBaseImageScan(ctx, in)
	if err == nil {
		s.due, s.published = false, s.published+1
	}
	return value, err
}

func TestProducedBaseFactCollectionRefreshesLeaseWhenDue(t *testing.T) {
	h, th := producedScanFixtureWithBase(t, false, true)
	root, err := th.store.GetCurrentDeploymentRegistryRootfs(t.Context(), th.app.AccountID, th.app.ID, th.dep.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	before, err := th.store.GetFreshBaseImageScan(t.Context(), root.Input.BaseProducerID, root.Input.BaseInputHash)
	if err != nil {
		t.Fatal(err)
	}
	store := &dueBaseRenewalStore{MemStore: th.store, due: true}
	h.store = store
	calls := 0
	h.WithGrypeRun(func(context.Context, string) (*ScanResult, error) { calls++; return producedScanResult(t, false), nil })
	if err := collectProducedFixtureScans(t, h, th); err != nil {
		t.Fatal(err)
	}
	after, err := th.store.GetFreshBaseImageScan(t.Context(), root.Input.BaseProducerID, root.Input.BaseInputHash)
	if err != nil || after.ID == before.ID || !after.ScannedAt.After(before.ScannedAt) || store.published != 1 || calls != 2 {
		t.Fatalf("fresh but due base lease was reused instead of renewed: %v", err)
	}
	// At this boundary the current evidence is still fresh; scheduling must
	// request a new scan rather than waiting for its authoritative expiry.
	now := time.Now().UTC()
	if !producedEvidenceRenewalDue(now, now.Add(api.ApplicationStandardArtifactScanRenewEvery), now) {
		t.Fatal("near-expiry evidence was not scheduled for renewal")
	}
}
