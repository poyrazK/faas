package state

// adr: 435

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/imagechain"
	"github.com/onebox-faas/faas/pkg/imagepublisher"
)

type registryVerificationTestStore interface {
	Store
	DeploymentRegistryVerificationStore
}
type registryEvidencePuller struct {
	digest     string
	attachment imagepublisher.ImageSignatureAttachment
}

func (p registryEvidencePuller) ResolveDigest(context.Context, string) (string, error) {
	return p.digest, nil
}
func (p registryEvidencePuller) FetchSignatureAttachments(context.Context, string, string) ([]imagepublisher.ImageSignatureAttachment, error) {
	return []imagepublisher.ImageSignatureAttachment{p.attachment}, nil
}

func registryVerificationFixture(t *testing.T, s registryVerificationTestStore, sidecar bool) (DeploymentRegistryVerificationInput, App, Deployment) {
	return registryVerificationFixtureWithChain(t, s, sidecar, nil)
}
func registryVerificationFixtureWithChain(t *testing.T, s registryVerificationTestStore, sidecar bool, chain *imagechain.Evidence) (DeploymentRegistryVerificationInput, App, Deployment) {
	t.Helper()
	owner, err := s.CreateAccountWithPersonalOrg(t.Context(), CreateAccountWithPersonalOrgParams{Email: uuid.NewString() + "@example.com", Plan: api.PlanPro})
	if err != nil {
		t.Fatal(err)
	}
	app, err := s.CreateApp(t.Context(), App{AccountID: owner.Account.ID, OrgID: owner.PersonalOrg.ID, Slug: "proof-" + uuid.NewString()[:8], RAMMB: 128, MaxConcurrency: 2})
	if err != nil {
		t.Fatal(err)
	}
	image := "registry.example/team/service:latest"
	workload := ""
	d := Deployment{AppID: app.ID, Kind: DeploymentKindImage, ImageDigest: image}
	if sidecar {
		workload = "metrics"
		image = "registry.example/team/metrics:latest"
		d.Sidecars = []byte(`[{"name":"metrics","image":"registry.example/team/metrics:latest","type":"sidecar","port":9090}]`)
	}
	dep, err := s.CreateDeployment(t.Context(), d)
	if err != nil {
		t.Fatal(err)
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.MarshalPKIXPublicKey(&key.PublicKey)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err = s.UpsertAppTrustedSigner(t.Context(), app.AccountID, app.ID, "company", der, app.AccountID); err != nil {
		t.Fatal(err)
	}
	// A caller cannot alter a current stored key through its upload buffer.
	der[0] ^= 1
	subject := "sha256:" + strings.Repeat("a", 64)
	selected := "sha256:" + strings.Repeat("b", 64)
	if chain != nil {
		subject = imagechain.Digest(chain.SourceManifest)
		selected = subject
		if len(chain.SelectedManifest) > 0 {
			selected = imagechain.Digest(chain.SelectedManifest)
		}
	}
	payload := []byte(fmt.Sprintf(`{"critical":{"identity":{"docker-reference":"registry.example/team/service"},"image":{"docker-manifest-digest":%q},"type":"cosign container image signature"}}`, subject))
	sum := sha256.Sum256(payload)
	signature, err := ecdsa.SignASN1(rand.Reader, key, sum[:])
	if err != nil {
		t.Fatal(err)
	}
	proof, err := imagepublisher.VerifyImageSignatureAttachments(t.Context(), registryEvidencePuller{subject, imagepublisher.ImageSignatureAttachment{
		ManifestDigest: "sha256:" + strings.Repeat("c", 64), PayloadDigest: fmt.Sprintf("sha256:%x", sum), Payload: payload, Signature: signature,
	}}, image, []imagepublisher.TrustedPublisher{{Name: "company", PublicKey: &key.PublicKey}})
	if err != nil {
		t.Fatal(err)
	}
	repo := strings.TrimSuffix(image, ":latest")
	return DeploymentRegistryVerificationInput{ID: uuid.NewString(), AccountID: app.AccountID, OrgID: app.OrgID, AppID: app.ID, DeploymentID: dep.ID,
		WorkloadName: workload, ImageReference: image, SourceReference: repo + "@" + subject, SelectedReference: repo + "@" + selected, SelectedDigest: selected, Proof: proof, ImageChain: chain}, app, dep
}

func registryVerificationLifecycle(t *testing.T, s registryVerificationTestStore) {
	for _, sidecar := range []bool{false, true} {
		t.Run(fmt.Sprint("sidecar=", sidecar), func(t *testing.T) {
			in, _, _ := registryVerificationFixture(t, s, sidecar)
			before := time.Now().UTC()
			v, err := s.RecordDeploymentRegistryVerification(t.Context(), in)
			if err != nil {
				t.Fatal(err)
			}
			if v.ID != in.ID || v.InputHash == "" || v.VerifiedAt.Before(before.Add(-time.Second)) || v.VerifiedAt.After(time.Now().Add(time.Second)) || v.ExpiresAt.Sub(v.VerifiedAt) != api.ImageSignatureVerificationTTL {
				t.Fatalf("storage clock/binding: %+v", v)
			}
			retry, err := s.RecordDeploymentRegistryVerification(t.Context(), in)
			if err != nil || retry.ID != v.ID || !retry.VerifiedAt.Equal(v.VerifiedAt) || !retry.ExpiresAt.Equal(v.ExpiresAt) {
				t.Fatalf("retry changed immutable evidence: %+v %v", retry, err)
			}
			// The store owns evidence buffers and returns fresh copies.
			in.Proof.Evidence.Payload[0] ^= 1
			v.Input.Proof.Evidence.Signature[0] ^= 1
			got, err := s.GetLatestDeploymentRegistryVerification(t.Context(), in.AccountID, in.AppID, in.DeploymentID, in.WorkloadName)
			if err != nil {
				t.Fatal(err)
			}
			signers, err := s.ListAppTrustedSigners(t.Context(), in.AccountID, in.AppID)
			if err != nil {
				t.Fatal(err)
			}
			if len(signers) != 1 || imagepublisher.ReverifyImageSignatureProof(got.Input.Proof, signers[0].CosignPublicKey) != nil {
				t.Fatal("stored proof was mutated through input/result alias")
			}
			// Read results also must not expose mutable stored-key buffers.
			signers[0].CosignPublicKey[0] ^= 1
			refreshed := cloneRegistryVerificationInput(got.Input)
			refreshed.ID = uuid.NewString()
			if _, err := s.RecordDeploymentRegistryVerification(t.Context(), refreshed); err != nil {
				t.Fatalf("read buffer mutated stored publisher: %v", err)
			}
			for _, wrong := range []string{"account", "app", "deployment", "workload"} {
				account, app, dep, workload := in.AccountID, in.AppID, in.DeploymentID, in.WorkloadName
				switch wrong {
				case "account":
					account = uuid.NewString()
				case "app":
					app = uuid.NewString()
				case "deployment":
					dep = uuid.NewString()
				case "workload":
					workload = "absent"
				}
				if _, err := s.GetLatestDeploymentRegistryVerification(t.Context(), account, app, dep, workload); !errors.Is(err, ErrNotFound) {
					t.Fatalf("%s scope exposed proof: %v", wrong, err)
				}
			}
			changed := cloneRegistryVerificationInput(got.Input)
			changed.SelectedDigest = "sha256:" + strings.Repeat("d", 64)
			changed.SelectedReference = strings.TrimSuffix(changed.SelectedReference, got.Input.SelectedDigest) + changed.SelectedDigest
			if _, err := s.RecordDeploymentRegistryVerification(t.Context(), changed); !errors.Is(err, ErrConflict) {
				t.Fatalf("same id accepted changed inputs: %v", err)
			}
		})
	}
}

func registryVerificationRejectsSubstitution(t *testing.T, s registryVerificationTestStore) {
	in, _, _ := registryVerificationFixture(t, s, false)
	for _, which := range []string{"org", "account", "tag", "workload", "source repository", "selected repository", "mutable source", "selected digest", "payload", "signature", "fingerprint", "missing evidence"} {
		t.Run(which, func(t *testing.T) {
			candidate := cloneRegistryVerificationInput(in)
			candidate.ID = uuid.NewString()
			switch which {
			case "org":
				candidate.OrgID = uuid.NewString()
			case "account":
				candidate.AccountID = uuid.NewString()
			case "tag":
				candidate.ImageReference = "registry.example/team/service:other"
			case "workload":
				candidate.WorkloadName = "missing"
			case "source repository":
				candidate.SourceReference = strings.Replace(candidate.SourceReference, "team/service", "other/service", 1)
			case "selected repository":
				candidate.SelectedReference = strings.Replace(candidate.SelectedReference, "team/service", "other/service", 1)
			case "mutable source":
				candidate.SourceReference = candidate.ImageReference
			case "selected digest":
				candidate.SelectedDigest = "sha256:" + strings.Repeat("f", 64)
			case "payload":
				candidate.Proof.Evidence.Payload[0] ^= 1
			case "signature":
				candidate.Proof.Evidence.Signature[0] ^= 1
			case "fingerprint":
				candidate.Proof.PublisherKeySHA256 = strings.Repeat("f", 64)
			case "missing evidence":
				candidate.Proof.Evidence = nil
			}
			if _, err := s.RecordDeploymentRegistryVerification(t.Context(), candidate); err == nil {
				t.Fatalf("accepted %s", which)
			}
		})
	}
	if _, err := s.GetLatestDeploymentRegistryVerification(t.Context(), in.AccountID, in.AppID, in.DeploymentID, ""); !errors.Is(err, ErrNotFound) {
		t.Fatalf("rejection left proof: %v", err)
	}
}

func registryVerificationCurrentKey(t *testing.T, s registryVerificationTestStore) {
	in, app, _ := registryVerificationFixture(t, s, false)
	if _, err := s.RecordDeploymentRegistryVerification(t.Context(), in); err != nil {
		t.Fatal(err)
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.MarshalPKIXPublicKey(&key.PublicKey)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err = s.UpsertAppTrustedSigner(t.Context(), app.AccountID, app.ID, "company", der, app.AccountID); err != nil {
		t.Fatal(err)
	}
	for _, sameID := range []bool{true, false} {
		candidate := cloneRegistryVerificationInput(in)
		if !sameID {
			candidate.ID = uuid.NewString()
		}
		if _, err := s.RecordDeploymentRegistryVerification(t.Context(), candidate); !errors.Is(err, imagepublisher.ErrSignatureInvalid) {
			t.Fatalf("rotated key accepted cache: %v", err)
		}
	}
	if err := s.DeleteAppTrustedSigner(t.Context(), app.AccountID, app.ID, "company"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.RecordDeploymentRegistryVerification(t.Context(), in); !errors.Is(err, imagepublisher.ErrSignatureInvalid) {
		t.Fatalf("deleted key accepted cache: %v", err)
	}
	// Historical retrieval supplies the immutable source for refresh; it never
	// claims current approval or resets the original storage-owned expiry.
	got, err := s.GetLatestDeploymentRegistryVerification(t.Context(), in.AccountID, in.AppID, in.DeploymentID, "")
	if err != nil || got.Input.SourceReference != in.SourceReference {
		t.Fatalf("history missing: %+v %v", got, err)
	}
	if err := s.UpdateDeploymentStatus(t.Context(), in.DeploymentID, DeployFailed, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := s.RecordDeploymentRegistryVerification(t.Context(), in); !errors.Is(err, ErrApplicationStandardRuntimeStale) {
		t.Fatalf("failed artifact gained fresh proof: %v", err)
	}
}

func TestMemRegistryVerificationLifecycle(t *testing.T) {
	registryVerificationLifecycle(t, NewMemStore())
}
func TestMemRegistryVerificationSubstitution(t *testing.T) {
	registryVerificationRejectsSubstitution(t, NewMemStore())
}
func TestMemRegistryVerificationCurrentKey(t *testing.T) {
	registryVerificationCurrentKey(t, NewMemStore())
}

func registryVerificationDeletionLifecycle(t *testing.T, s registryVerificationTestStore) {
	in, app, _ := registryVerificationFixture(t, s, false)
	recorded, err := s.RecordDeploymentRegistryVerification(t.Context(), in)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.ScheduleAppDeletion(t.Context(), app.ID, time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetLatestDeploymentRegistryVerification(t.Context(), in.AccountID, in.AppID, in.DeploymentID, ""); !errors.Is(err, ErrNotFound) {
		t.Fatalf("deleted app exposed evidence: %v", err)
	}
	limits, _ := api.LimitsFor(api.PlanPro)
	if _, err := s.RestoreApp(t.Context(), app.ID, limits); err != nil {
		t.Fatal(err)
	}
	restored, err := s.GetLatestDeploymentRegistryVerification(t.Context(), in.AccountID, in.AppID, in.DeploymentID, "")
	if err != nil || restored.ID != recorded.ID || !restored.ExpiresAt.Equal(recorded.ExpiresAt) {
		t.Fatalf("restore erased or refreshed immutable history: %+v %v", restored, err)
	}
	if _, err := s.ScheduleAppDeletion(t.Context(), app.ID, time.Now().Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err := s.ClaimAppDeletion(t.Context(), app.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteAppPermanently(t.Context(), app.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetLatestDeploymentRegistryVerification(t.Context(), in.AccountID, in.AppID, in.DeploymentID, ""); !errors.Is(err, ErrNotFound) {
		t.Fatalf("erased owner exposed history: %v", err)
	}
	if m, ok := s.(*MemStore); ok {
		m.mu.Lock()
		defer m.mu.Unlock()
		for _, value := range m.deploymentRegistryVerifications {
			if sameStandardUUID(value.Input.AppID, app.ID) {
				t.Fatal("owner evidence retained after erasure")
			}
		}
	}
}
func TestMemRegistryVerificationDeletionLifecycle(t *testing.T) {
	registryVerificationDeletionLifecycle(t, NewMemStore())
}
