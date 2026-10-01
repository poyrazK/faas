package state

// adr: 393

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/imagepublisher"
)

type registryRootfsTestStore interface {
	registryVerificationTestStore
	DeploymentRegistryRootfsStore
}

func registryRootfsFixture(t *testing.T, s registryRootfsTestStore, sidecar bool) (DeploymentRegistryRootfsInput, DeploymentRegistryVerification, App, Deployment) {
	t.Helper()
	in, app, dep := registryVerificationFixture(t, s, sidecar)
	parent, err := s.RecordDeploymentRegistryVerification(t.Context(), in)
	if err != nil {
		t.Fatal(err)
	}
	root := DeploymentRegistryRootfsInput{ID: uuid.NewString(), RegistryVerificationID: parent.ID, RegistryInputHash: parent.InputHash,
		AccountID: app.AccountID, OrgID: app.OrgID, AppID: app.ID, DeploymentID: dep.ID, WorkloadName: in.WorkloadName, Scope: dep.Scope,
		Kind: "app-layer", StorageKey: "apps/" + app.Slug + "/" + dep.ID + ".ext4", RootfsPath: "/srv/apps/" + dep.ID + ".ext4", ContentBytes: 123,
		ArtifactDigest: "sha256:" + strings.Repeat("d", 64), ArtifactBytes: 4096}
	if sidecar {
		root.Kind = "sidecar-layer"
		root.RootfsPath = ""
		root.StorageKey += ".metrics"
	}
	return root, parent, app, dep
}

func registryRootfsLifecycle(t *testing.T, s registryRootfsTestStore) {
	for _, kind := range []string{"app-layer", "full-rootfs", "sidecar-layer"} {
		t.Run(kind, func(t *testing.T) {
			in, parent, _, _ := registryRootfsFixture(t, s, kind == "sidecar-layer")
			in.Kind = kind
			// A later conversion selected a different child. Publication must
			// retain this conversion's exact parent instead of selecting latest.
			later := cloneRegistryVerificationInput(parent.Input)
			later.ID = uuid.NewString()
			later.SelectedDigest = "sha256:" + strings.Repeat("e", 64)
			later.SelectedReference = strings.TrimSuffix(later.SelectedReference, parent.Input.SelectedDigest) + later.SelectedDigest
			if _, err := s.RecordDeploymentRegistryVerification(t.Context(), later); err != nil {
				t.Fatal(err)
			}
			before := time.Now()
			value, err := s.PublishDeploymentRegistryRootfs(t.Context(), in)
			if err != nil {
				t.Fatal(err)
			}
			if value.Input.RegistryVerificationID != parent.ID || value.Input.RegistryInputHash != parent.InputHash || !value.ExpiresAt.Equal(parent.ExpiresAt) || value.PublishedAt.Before(before.Add(-time.Second)) || value.PublishedAt.After(time.Now().Add(time.Second)) {
				t.Fatalf("lineage/clock mismatch: %+v", value)
			}
			got, err := s.GetCurrentDeploymentRegistryRootfs(t.Context(), in.AccountID, in.AppID, in.DeploymentID, in.WorkloadName)
			if err != nil || got.ID != value.ID || got.Input.ArtifactDigest != in.ArtifactDigest || got.Input.ArtifactBytes != in.ArtifactBytes {
				t.Fatalf("selection lost exact producer: %+v %v", got, err)
			}
			if in.WorkloadName != "" {
				layers, err := s.ListDeploymentSidecarLayers(t.Context(), in.DeploymentID)
				if err != nil || len(layers) != 1 || layers[0].ContentDigest != parent.Input.SelectedReference || layers[0].Bytes != in.ContentBytes {
					t.Fatalf("sidecar metadata not atomic/exact: %+v %v", layers, err)
				}
			} else {
				dep, err := s.DeploymentByID(t.Context(), in.DeploymentID)
				if err != nil || dep.RootfsKey != in.StorageKey || dep.RootfsBytes != in.ContentBytes || dep.ImageDigest != parent.Input.ImageReference {
					t.Fatalf("main metadata not atomic/intent retained: %+v %v", dep, err)
				}
			}
			retry, err := s.PublishDeploymentRegistryRootfs(t.Context(), in)
			if err != nil || retry.ID != value.ID || !retry.PublishedAt.Equal(value.PublishedAt) || !retry.ExpiresAt.Equal(value.ExpiresAt) {
				t.Fatalf("retry refreshed proof: %+v %v", retry, err)
			}
			changed := in
			changed.ArtifactBytes++
			if _, err := s.PublishDeploymentRegistryRootfs(t.Context(), changed); !errors.Is(err, ErrConflict) {
				t.Fatalf("same id changed producer bytes: %v", err)
			}
			changed.ID = uuid.NewString()
			if _, err := s.PublishDeploymentRegistryRootfs(t.Context(), changed); err != nil {
				t.Fatal(err)
			}
			if _, err := s.PublishDeploymentRegistryRootfs(t.Context(), in); !errors.Is(err, ErrConflict) {
				t.Fatalf("old retry reactivated superseded producer: %v", err)
			}
			for _, scope := range []string{"account", "app", "deployment", "workload"} {
				account, app, dep, workload := in.AccountID, in.AppID, in.DeploymentID, in.WorkloadName
				switch scope {
				case "account":
					account = uuid.NewString()
				case "app":
					app = uuid.NewString()
				case "deployment":
					dep = uuid.NewString()
				case "workload":
					workload = "missing"
				}
				if _, err := s.GetCurrentDeploymentRegistryRootfs(t.Context(), account, app, dep, workload); !errors.Is(err, ErrNotFound) {
					t.Fatalf("%s exposed producer: %v", scope, err)
				}
			}
		})
	}
}

func registryRootfsRejectsSubstitution(t *testing.T, s registryRootfsTestStore) {
	in, _, _, _ := registryRootfsFixture(t, s, false)
	for _, field := range []string{"verification", "parent hash", "account", "org", "app", "deployment", "workload", "scope", "empty artifact", "mutable digest"} {
		t.Run(field, func(t *testing.T) {
			candidate := in
			candidate.ID = uuid.NewString()
			switch field {
			case "verification":
				candidate.RegistryVerificationID = uuid.NewString()
			case "parent hash":
				candidate.RegistryInputHash = strings.Repeat("f", 64)
			case "account":
				candidate.AccountID = uuid.NewString()
			case "org":
				candidate.OrgID = uuid.NewString()
			case "app":
				candidate.AppID = uuid.NewString()
			case "deployment":
				candidate.DeploymentID = uuid.NewString()
			case "workload":
				candidate.WorkloadName = "other"
				candidate.Kind = "sidecar-layer"
				candidate.RootfsPath = ""
			case "scope":
				candidate.Scope = "production"
			case "empty artifact":
				candidate.ArtifactBytes = 0
			case "mutable digest":
				candidate.ArtifactDigest = "latest"
			}
			if _, err := s.PublishDeploymentRegistryRootfs(t.Context(), candidate); err == nil {
				t.Fatalf("accepted %s substitution", field)
			}
		})
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := s.PublishDeploymentRegistryRootfs(ctx, in); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled publication: %v", err)
	}
	assertNoRegistryRootfs(t, s, in)
}

func assertNoRegistryRootfs(t *testing.T, s registryRootfsTestStore, in DeploymentRegistryRootfsInput) {
	t.Helper()
	if _, err := s.GetCurrentDeploymentRegistryRootfs(t.Context(), in.AccountID, in.AppID, in.DeploymentID, in.WorkloadName); !errors.Is(err, ErrNotFound) {
		t.Fatalf("failed producer left selection: %v", err)
	}
	dep, err := s.DeploymentByID(t.Context(), in.DeploymentID)
	if err != nil || dep.RootfsKey != "" || dep.RootfsBytes != 0 || dep.RootfsPath != "" {
		t.Fatalf("failed publication changed metadata: %+v %v", dep, err)
	}
}

func registryRootfsCurrentKey(t *testing.T, s registryRootfsTestStore) {
	for _, mode := range []string{"revoked", "rotated"} {
		t.Run(mode, func(t *testing.T) {
			in, _, app, _ := registryRootfsFixture(t, s, false)
			if mode == "revoked" {
				if err := s.DeleteAppTrustedSigner(t.Context(), app.AccountID, app.ID, "company"); err != nil {
					t.Fatal(err)
				}
			} else {
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
			}
			if _, err := s.PublishDeploymentRegistryRootfs(t.Context(), in); !errors.Is(err, imagepublisher.ErrSignatureInvalid) {
				t.Fatalf("obsolete publisher produced rootfs: %v", err)
			}
			assertNoRegistryRootfs(t, s, in)
		})
	}
}

func registryRootfsDeletionLifecycle(t *testing.T, s registryRootfsTestStore) {
	in, _, app, _ := registryRootfsFixture(t, s, false)
	value, err := s.PublishDeploymentRegistryRootfs(t.Context(), in)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.ScheduleAppDeletion(t.Context(), app.ID, time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetCurrentDeploymentRegistryRootfs(t.Context(), in.AccountID, in.AppID, in.DeploymentID, ""); !errors.Is(err, ErrNotFound) {
		t.Fatalf("deleted owner exposed rootfs: %v", err)
	}
	if _, err := s.RestoreApp(t.Context(), app.ID, api.MustLimitsFor(api.PlanPro)); err != nil {
		t.Fatal(err)
	}
	got, err := s.GetCurrentDeploymentRegistryRootfs(t.Context(), in.AccountID, in.AppID, in.DeploymentID, "")
	if err != nil || got.ID != value.ID || !got.ExpiresAt.Equal(value.ExpiresAt) {
		t.Fatalf("restore erased/refreshed rootfs: %+v %v", got, err)
	}
	if err := s.DeleteAppTrustedSigner(t.Context(), app.AccountID, app.ID, "company"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetCurrentDeploymentRegistryRootfs(t.Context(), in.AccountID, in.AppID, in.DeploymentID, ""); err != nil {
		t.Fatalf("historical evidence lost after revocation: %v", err)
	}
	if _, err := s.PublishDeploymentRegistryRootfs(t.Context(), in); !errors.Is(err, imagepublisher.ErrSignatureInvalid) {
		t.Fatalf("history became renewed authority: %v", err)
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
	if _, err := s.GetCurrentDeploymentRegistryRootfs(t.Context(), in.AccountID, in.AppID, in.DeploymentID, ""); !errors.Is(err, ErrNotFound) {
		t.Fatalf("erased owner exposed rootfs: %v", err)
	}
}

func TestMemRegistryRootfsLifecycle(t *testing.T) { registryRootfsLifecycle(t, NewMemStore()) }
func TestMemRegistryRootfsSubstitution(t *testing.T) {
	registryRootfsRejectsSubstitution(t, NewMemStore())
}
func TestMemRegistryRootfsCurrentKey(t *testing.T) { registryRootfsCurrentKey(t, NewMemStore()) }
func TestMemRegistryRootfsDeletionLifecycle(t *testing.T) {
	registryRootfsDeletionLifecycle(t, NewMemStore())
}
func TestMemRegistryRootfsOwnerChangeHidesSelection(t *testing.T) {
	s := NewMemStore()
	in, _, app, _ := registryRootfsFixture(t, s, false)
	if _, err := s.PublishDeploymentRegistryRootfs(t.Context(), in); err != nil {
		t.Fatal(err)
	}
	s.mu.Lock()
	app.AccountID = uuid.NewString()
	s.apps[app.ID] = app
	s.mu.Unlock()
	if _, err := s.GetCurrentDeploymentRegistryRootfs(t.Context(), app.AccountID, app.ID, in.DeploymentID, ""); !errors.Is(err, ErrNotFound) {
		t.Fatalf("new owner inherited prior publisher's producer evidence: %v", err)
	}
}
func TestMemRegistryRootfsExpiredAndCancelled(t *testing.T) {
	for _, mode := range []string{"expired", "cancelled"} {
		t.Run(mode, func(t *testing.T) {
			s := NewMemStore()
			in, parent, _, _ := registryRootfsFixture(t, s, false)
			if mode == "expired" {
				s.mu.Lock()
				parent.VerifiedAt = time.Now().Add(-25 * time.Hour)
				parent.ExpiresAt = parent.VerifiedAt.Add(api.ImageSignatureVerificationTTL)
				s.deploymentRegistryVerifications[parent.ID] = parent
				s.mu.Unlock()
			} else if err := s.UpdateDeploymentStatus(t.Context(), in.DeploymentID, DeployCancelled, ""); err != nil {
				t.Fatal(err)
			}
			if _, err := s.PublishDeploymentRegistryRootfs(t.Context(), in); !errors.Is(err, ErrApplicationStandardRuntimeStale) {
				t.Fatalf("late publication: %v", err)
			}
			assertNoRegistryRootfs(t, s, in)
		})
	}
}
