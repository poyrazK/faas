package state

// adr: 393

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/imagepublisher"
)

func artifactScanRenewal(t *testing.T, s artifactScanTestStore) {
	for _, sidecar := range []bool{false, true} {
		t.Run(map[bool]string{false: "main", true: "sidecar"}[sidecar], func(t *testing.T) {
			in, root, app, dep := artifactScanFixture(t, s, sidecar)
			origin, err := s.GetDeploymentRegistryVerificationByID(t.Context(), app.AccountID, app.ID, dep.ID, root.Input.RegistryVerificationID)
			if err != nil {
				t.Fatal(err)
			}
			if err := s.UpdateDeploymentStatus(t.Context(), dep.ID, DeployLive, ""); err != nil {
				t.Fatal(err)
			}
			refresh := origin.Input
			refresh.ID = uuid.NewString()
			approved, err := s.RecordDeploymentRegistryVerification(t.Context(), refresh)
			if err != nil {
				t.Fatal(err)
			}
			in.RegistryVerificationID, in.RegistryInputHash = approved.ID, approved.InputHash
			value, err := s.PublishDeploymentArtifactScan(t.Context(), in)
			if err != nil || value.Input.RegistryVerificationID == root.Input.RegistryVerificationID || value.ExpiresAt.After(approved.ExpiresAt) {
				t.Fatalf("new scan lost separate signature approval: %v", err)
			}
			retry, err := s.PublishDeploymentArtifactScan(t.Context(), in)
			if err != nil || !retry.ScannedAt.Equal(value.ScannedAt) || !retry.ExpiresAt.Equal(value.ExpiresAt) {
				t.Fatalf("retry extended renewed scan: %v", err)
			}
			got, err := s.GetCurrentDeploymentRegistryRootfs(t.Context(), app.AccountID, app.ID, dep.ID, in.WorkloadName)
			if err != nil || got.ID != root.ID || got.InputHash != root.InputHash || !got.PublishedAt.Equal(root.PublishedAt) || !got.ExpiresAt.Equal(root.ExpiresAt) {
				t.Fatalf("signature/scan renewal rewrote conversion lineage: %v", err)
			}
			old, err := s.GetDeploymentRegistryVerificationByID(t.Context(), app.AccountID, app.ID, dep.ID, origin.ID)
			if err != nil || !old.VerifiedAt.Equal(origin.VerifiedAt) || !old.ExpiresAt.Equal(origin.ExpiresAt) {
				t.Fatalf("origin signature clock changed: %v", err)
			}
			if _, err := s.GetDeploymentRegistryVerificationByID(t.Context(), uuid.NewString(), app.ID, dep.ID, origin.ID); !errors.Is(err, ErrNotFound) {
				t.Fatalf("exact origin getter crossed tenancy: %v", err)
			}
			old.Input.ImageChain.Config[0] ^= 1
			again, err := s.GetDeploymentRegistryVerificationByID(t.Context(), app.AccountID, app.ID, dep.ID, origin.ID)
			if err != nil || again.Input.ImageChain.Config[0] == old.Input.ImageChain.Config[0] {
				t.Fatalf("origin read aliased private bytes: %v", err)
			}
			for _, field := range []string{"approval hash", "missing hash", "missing id", "different source", "different config"} {
				t.Run(field, func(t *testing.T) {
					bad := in
					bad.ID = uuid.NewString()
					switch field {
					case "approval hash":
						bad.RegistryInputHash = strings.Repeat("0", 64)
					case "missing hash":
						bad.RegistryInputHash = ""
					case "missing id":
						bad.RegistryVerificationID = ""
					case "different source", "different config":
						changed := cloneRegistryVerificationInput(approved.Input)
						if field == "different source" {
							changed.SourceReference += "changed"
						} else {
							changed.ImageChain.Config[0] ^= 1
						}
						if sameRegistryScanSource(origin.Input, changed) {
							t.Fatal("different immutable source accepted for renewal")
						}
						return
					}
					if _, err := s.PublishDeploymentArtifactScan(t.Context(), bad); err == nil {
						t.Fatal("unbound renewal accepted")
					}
				})
			}
		})
	}
}

func artifactScanRenewalCurrentKey(t *testing.T, s artifactScanTestStore) {
	in, root, app, dep := artifactScanFixture(t, s, false)
	origin, err := s.GetDeploymentRegistryVerificationByID(t.Context(), app.AccountID, app.ID, dep.ID, root.Input.RegistryVerificationID)
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
	if _, _, err := s.UpsertAppTrustedSigner(t.Context(), app.AccountID, app.ID, "company", der, app.AccountID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.PublishDeploymentArtifactScan(t.Context(), in); err == nil {
		t.Fatal("origin signer rotation retained current approval")
	}
	payload := origin.Input.Proof.Evidence.Payload
	sum := sha256.Sum256(payload)
	sig, err := ecdsa.SignASN1(rand.Reader, key, sum[:])
	if err != nil {
		t.Fatal(err)
	}
	proof, err := imagepublisher.VerifyImageSignatureAttachments(t.Context(), registryEvidencePuller{origin.Input.Proof.SubjectDigest, imagepublisher.ImageSignatureAttachment{ManifestDigest: origin.Input.Proof.AttachmentManifestDigest, PayloadDigest: origin.Input.Proof.PayloadDigest, Payload: payload, Signature: sig}}, origin.Input.SourceReference, []imagepublisher.TrustedPublisher{{Name: "company", PublicKey: &key.PublicKey}})
	if err != nil {
		t.Fatal(err)
	}
	refresh := origin.Input
	refresh.ID, refresh.Proof = uuid.NewString(), proof
	approved, err := s.RecordDeploymentRegistryVerification(t.Context(), refresh)
	if err != nil {
		t.Fatal(err)
	}
	in.RegistryVerificationID, in.RegistryInputHash = approved.ID, approved.InputHash
	if _, err := s.PublishDeploymentArtifactScan(t.Context(), in); err != nil {
		t.Fatalf("current approved signer for same origin refused: %v", err)
	}
	if err := s.DeleteAppTrustedSigner(t.Context(), app.AccountID, app.ID, "company"); err != nil {
		t.Fatal(err)
	}
	in.ID = uuid.NewString()
	if _, err := s.PublishDeploymentArtifactScan(t.Context(), in); err == nil {
		t.Fatal("deleted current approval key renewed a scan")
	}
}

func TestMemArtifactScanRenewal(t *testing.T) { artifactScanRenewal(t, NewMemStore()) }
func TestMemArtifactScanRenewalCurrentKey(t *testing.T) {
	artifactScanRenewalCurrentKey(t, NewMemStore())
}

func artifactScanRenewalRejectsForeignApproval(t *testing.T, s artifactScanTestStore) {
	in, _, app, dep := artifactScanFixture(t, s, false)
	before, err := s.PublishDeploymentArtifactScan(t.Context(), in)
	if err != nil {
		t.Fatal(err)
	}
	for _, sidecar := range []bool{false, true} {
		_, foreign, owner, other := artifactScanFixture(t, s, sidecar)
		proof, err := s.GetDeploymentRegistryVerificationByID(t.Context(), owner.AccountID, owner.ID, other.ID, foreign.Input.RegistryVerificationID)
		if err != nil {
			t.Fatal(err)
		}
		bad := in
		bad.ID, bad.RegistryVerificationID, bad.RegistryInputHash = uuid.NewString(), proof.ID, proof.InputHash
		if _, err := s.PublishDeploymentArtifactScan(t.Context(), bad); !errors.Is(err, ErrApplicationStandardRuntimeStale) && !errors.Is(err, ErrNotFound) {
			t.Fatalf("foreign signature approval was accepted: %v", err)
		}
		if _, err := s.GetDeploymentRegistryVerificationByID(t.Context(), app.AccountID, app.ID, dep.ID, proof.ID); !errors.Is(err, ErrNotFound) {
			t.Fatalf("exact proof getter crossed owner/workload: %v", err)
		}
	}
	after, err := s.GetCurrentDeploymentArtifactScan(t.Context(), app.AccountID, app.ID, dep.ID, "")
	if err != nil || after.ID != before.ID || !after.ScannedAt.Equal(before.ScannedAt) || !after.ExpiresAt.Equal(before.ExpiresAt) {
		t.Fatalf("foreign approval changed selected scan: %v", err)
	}
}

func TestMemArtifactScanRenewalRejectsForeignApproval(t *testing.T) {
	artifactScanRenewalRejectsForeignApproval(t, NewMemStore())
}

func TestMemArtifactScanRenewsExpiredOrigin(t *testing.T) {
	s := NewMemStore()
	in, root, app, dep := artifactScanFixture(t, s, false)
	// Model a historical conversion and its now-expired signature. No actual
	// ext4/scanner/native-consumer proof is claimed by this clock fixture.
	s.mu.Lock()
	origin := s.deploymentRegistryVerifications[root.Input.RegistryVerificationID]
	origin.VerifiedAt = time.Now().Add(-48 * time.Hour).UTC()
	origin.ExpiresAt = origin.VerifiedAt.Add(24 * time.Hour)
	s.deploymentRegistryVerifications[origin.ID] = origin
	root.PublishedAt, root.ExpiresAt = origin.VerifiedAt.Add(time.Second), origin.ExpiresAt
	s.deploymentRegistryRootfs[root.ID] = root
	s.mu.Unlock()
	if _, err := s.PublishDeploymentArtifactScan(t.Context(), in); !errors.Is(err, ErrApplicationStandardRuntimeStale) {
		t.Fatalf("expired original proof renewed legacy scan: %v", err)
	}
	fresh := origin.Input
	fresh.ID = uuid.NewString()
	approved, err := s.RecordDeploymentRegistryVerification(t.Context(), fresh)
	if err != nil {
		t.Fatal(err)
	}
	in.RegistryVerificationID, in.RegistryInputHash = approved.ID, approved.InputHash
	value, err := s.PublishDeploymentArtifactScan(t.Context(), in)
	got, readErr := s.GetCurrentDeploymentRegistryRootfs(t.Context(), app.AccountID, app.ID, dep.ID, "")
	if err != nil || readErr != nil || !value.ExpiresAt.After(root.ExpiresAt) || !got.ExpiresAt.Equal(root.ExpiresAt) || got.ID != root.ID {
		t.Fatalf("fresh approval did not preserve expired conversion origin: %v %v", err, readErr)
	}
}
