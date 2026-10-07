package state

// adr: 435. Portable signature/store contracts, not native consumer evidence.

import (
	"crypto/ecdsa"
	"crypto/rand"
	"crypto/sha256"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/buildpublisher"
	"github.com/onebox-faas/faas/pkg/imagepublisher"
)

type standardPublisherTestStore interface {
	standardAutomaticTestStore
	BuildExportPublicationStore
	artifactScanTestStore
}

func TestMemApplicationStandardPublisherInheritance(t *testing.T) {
	standardPublisherInheritance(t, NewMemStore())
}
func TestMemApplicationStandardPublisherSourceAlias(t *testing.T) {
	standardPublisherSourceAlias(t, NewMemStore())
}
func TestMemApplicationStandardPublisherRegistryAlias(t *testing.T) {
	standardPublisherRegistryAlias(t, NewMemStore())
}

func standardPublisherInheritance(t *testing.T, s standardPublisherTestStore) {
	t.Helper()
	f := standardAutomaticSetup(t, s, false, false)
	for _, kind := range []string{"source-app", "source-function", "registry-main", "registry-sidecar"} {
		t.Run(kind, func(t *testing.T) {
			app := inheritedPublisherApp(t, s, f, kind)
			if strings.HasPrefix(kind, "source-") {
				in, dep, b := inheritedBuildExport(t, s, app, f.publisherKey)
				v, err := s.RecordBuildExportPublication(t.Context(), in)
				if err != nil {
					t.Fatal("company publisher was not inherited by source service", err)
				}
				completeBuildExportFixture(t, s, in, b)
				fresh, err := s.GetFreshBuildExportPublication(t.Context(), app.AccountID, app.ID, dep.ID, b.ID)
				if err != nil || fresh.ID != v.ID || !fresh.VerifiedAt.Equal(v.VerifiedAt) || !fresh.ExpiresAt.Equal(v.ExpiresAt) {
					t.Fatal("inherited source approval changed its identity or clocks", err)
				}
			} else {
				in := inheritedRegistryInput(t, s, app, f.publisherKey, kind == "registry-sidecar")
				v, err := s.RecordDeploymentRegistryVerification(t.Context(), in)
				if err != nil || v.Input.Proof.PublisherName != "company" {
					t.Fatal("company publisher was not inherited by registry workload", err)
				}
			}
		})
	}
}

func inheritedPublisherApp(t *testing.T, s standardPublisherTestStore, f standardAutomaticFixture, kind string) App {
	t.Helper()
	in := App{AccountID: f.owner.Account.ID, OrgID: f.owner.PersonalOrg.ID, Slug: kind, RAMMB: 128}
	if kind == "source-function" {
		in.Type, in.Runtime = AppTypeFunction, "node22"
	}
	app, err := s.CreateApp(t.Context(), in)
	if err != nil {
		t.Fatal(err)
	}
	if e := automaticRepair(t, s); e.State != "persisted" || e.ObservedRevision != 0 {
		t.Fatal("new service enrollment was not persisted without a fabricated ACK", e)
	}
	app, err = s.AppByID(t.Context(), app.ID)
	if err != nil || !app.RequireSigned {
		t.Fatal("new service did not inherit mandatory signing", err)
	}
	keys, err := s.ListAppTrustedSignersForApp(t.Context(), app.ID)
	fingerprint, fingerprintErr := imagepublisher.PublisherKeySHA256(&f.publisherKey.PublicKey)
	if err != nil || fingerprintErr != nil || len(keys) != 1 || !strings.HasPrefix(keys[0].SignerName, "standard-") || standardReviewBytesDigest(keys[0].CosignPublicKey) != fingerprint {
		t.Fatal("new service did not receive the approved company key", err, fingerprintErr)
	}
	if _, _, err := s.UpsertAppTrustedSigner(t.Context(), app.AccountID, app.ID, "company", keys[0].CosignPublicKey, app.AccountID); !errors.Is(err, ErrApplicationStandardManagedControl) {
		t.Fatal("mandatory publisher accepted manual enrollment", err)
	}
	return app
}

func inheritedBuildExport(t *testing.T, s buildExportTestStore, app App, key *ecdsa.PrivateKey) (BuildExportPublicationInput, Deployment, Build) {
	t.Helper()
	dep, err := s.CreateDeployment(t.Context(), Deployment{AppID: app.ID, Kind: DeploymentKindTarball, SourceSHA256: strings.Repeat("a", 64)})
	if err != nil {
		t.Fatal(err)
	}
	b, err := s.CreateBuild(t.Context(), dep.ID, dep.Kind, 42, "")
	if err != nil {
		t.Fatal(err)
	}
	b, err = s.ClaimQueuedBuild(t.Context(), b.ID)
	if err != nil {
		t.Fatal(err)
	}
	c := buildpublisher.Claims{Format: buildpublisher.Format, AccountID: canonicalStandardUUID(app.AccountID), OrgID: registryCanonicalOrg(app.OrgID), AppID: canonicalStandardUUID(app.ID), DeploymentID: canonicalStandardUUID(dep.ID), BuildID: canonicalStandardUUID(b.ID), ClaimStartedAt: b.StartedAt.UTC().Format(time.RFC3339Nano), SourceSHA256: dep.SourceSHA256, ExportDigest: "sha256:" + strings.Repeat("b", 64), ExportBytes: 42, Runtime: app.Runtime, BuilderNodeID: "builder-one"}
	proof, err := buildpublisher.Sign(t.Context(), c, "company", key)
	if err != nil {
		t.Fatal(err)
	}
	return BuildExportPublicationInput{ID: uuid.NewString(), Claims: c, Proof: proof}, dep, b
}

func inheritedRegistryInput(t *testing.T, s registryVerificationTestStore, app App, key *ecdsa.PrivateKey, sidecar bool) DeploymentRegistryVerificationInput {
	t.Helper()
	image := "registry.example/team/service:latest"
	d := Deployment{AppID: app.ID, Kind: DeploymentKindImage, ImageDigest: image}
	workload := ""
	if sidecar {
		workload, image = "metrics", "registry.example/team/metrics:latest"
		d.Sidecars = []byte(`[{"name":"metrics","image":"registry.example/team/metrics:latest","type":"sidecar","port":9090}]`)
	}
	dep, err := s.CreateDeployment(t.Context(), d)
	if err != nil {
		t.Fatal(err)
	}
	subject := "sha256:" + strings.Repeat("a", 64)
	payload := []byte(fmt.Sprintf(`{"critical":{"identity":{"docker-reference":"registry.example/team/service"},"image":{"docker-manifest-digest":%q},"type":"cosign container image signature"}}`, subject))
	sum := sha256.Sum256(payload)
	signature, err := ecdsa.SignASN1(rand.Reader, key, sum[:])
	if err != nil {
		t.Fatal(err)
	}
	proof, err := imagepublisher.VerifyImageSignatureAttachments(t.Context(), registryEvidencePuller{subject, imagepublisher.ImageSignatureAttachment{ManifestDigest: "sha256:" + strings.Repeat("c", 64), PayloadDigest: fmt.Sprintf("sha256:%x", sum), Payload: payload, Signature: signature}}, image, []imagepublisher.TrustedPublisher{{Name: "company", PublicKey: &key.PublicKey}})
	if err != nil {
		t.Fatal(err)
	}
	reference := strings.TrimSuffix(image, ":latest") + "@" + subject
	return DeploymentRegistryVerificationInput{ID: uuid.NewString(), AccountID: app.AccountID, OrgID: app.OrgID, AppID: app.ID, DeploymentID: dep.ID, WorkloadName: workload, ImageReference: image, SourceReference: reference, SelectedReference: reference, SelectedDigest: subject, Proof: proof}
}

func aliasApprovedPublisher(t *testing.T, s Store, app App) (string, []byte) {
	t.Helper()
	keys, err := s.ListAppTrustedSignersForApp(t.Context(), app.ID)
	if err != nil || len(keys) != 1 {
		t.Fatal("fixture approved key missing", err)
	}
	alias := "standard-" + uuid.NewString()
	if _, _, err := s.UpsertAppTrustedSigner(t.Context(), app.AccountID, app.ID, alias, keys[0].CosignPublicKey, app.AccountID); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteAppTrustedSigner(t.Context(), app.AccountID, app.ID, keys[0].SignerName); err != nil {
		t.Fatal(err)
	}
	return alias, keys[0].CosignPublicKey
}

func installForeignPublisher(t *testing.T, s Store, current App, der []byte) {
	t.Helper()
	owner, err := s.CreateAccountWithPersonalOrg(t.Context(), CreateAccountWithPersonalOrgParams{Email: uuid.NewString() + "@example.com", Plan: api.PlanPro})
	if err != nil {
		t.Fatal(err)
	}
	app, err := s.CreateApp(t.Context(), App{AccountID: owner.Account.ID, OrgID: owner.PersonalOrg.ID, Slug: "foreign-" + uuid.NewString()[:8], RAMMB: 128})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.UpsertAppTrustedSigner(t.Context(), app.AccountID, app.ID, "company", der, app.AccountID); err != nil {
		t.Fatal(err)
	}
	nearby, err := s.CreateApp(t.Context(), App{AccountID: current.AccountID, OrgID: current.OrgID, Slug: "nearby-" + uuid.NewString()[:8], RAMMB: 128})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.UpsertAppTrustedSigner(t.Context(), nearby.AccountID, nearby.ID, "company", der, nearby.AccountID); err != nil {
		t.Fatal(err)
	}
}

func standardPublisherSourceAlias(t *testing.T, s buildExportTestStore) {
	t.Helper()
	in, app, dep, b, _ := buildExportFixture(t, s)
	alias, der := aliasApprovedPublisher(t, s, app)
	value, err := s.RecordBuildExportPublication(t.Context(), in)
	if err != nil {
		t.Fatal("display label remained a source authority requirement", err)
	}
	completeBuildExportFixture(t, s, in, b)
	fresh, err := s.GetFreshBuildExportPublication(t.Context(), app.AccountID, app.ID, dep.ID, b.ID)
	if err != nil || fresh.ID != value.ID || !fresh.VerifiedAt.Equal(value.VerifiedAt) || !fresh.ExpiresAt.Equal(value.ExpiresAt) {
		t.Fatal("alias changed retained approval clocks", err)
	}
	forged := in
	forged.ID, forged.Proof = uuid.NewString(), in.Proof.Clone()
	forged.Proof.Signature[len(forged.Proof.Signature)-1] ^= 1
	forged.Proof.SignatureDigest = fmt.Sprintf("sha256:%x", sha256.Sum256(forged.Proof.Signature))
	if buildpublisher.CheckProof(forged.Claims, forged.Proof) != nil {
		t.Fatal("forged fixture did not reach signature authentication")
	}
	if _, err := s.RecordBuildExportPublication(t.Context(), forged); !errors.Is(err, buildpublisher.ErrInvalid) {
		t.Fatal("approved fingerprint admitted a forged signature", err)
	}
	if err := s.DeleteAppTrustedSigner(t.Context(), app.AccountID, app.ID, alias); err != nil {
		t.Fatal(err)
	}
	installForeignPublisher(t, s, app, der)
	if _, err := s.GetFreshBuildExportPublication(t.Context(), app.AccountID, app.ID, dep.ID, b.ID); !errors.Is(err, buildpublisher.ErrInvalid) {
		t.Fatal("foreign approval substituted for revoked scoped key", err)
	}
}

func standardPublisherRegistryAlias(t *testing.T, s artifactScanTestStore) {
	t.Helper()
	for _, sidecar := range []bool{false, true} {
		t.Run(fmt.Sprint("sidecar=", sidecar), func(t *testing.T) {
			in, root, app, _ := artifactScanFixture(t, s, sidecar)
			alias, der := aliasApprovedPublisher(t, s, app)
			retry, err := s.PublishDeploymentRegistryRootfs(t.Context(), root.Input)
			if err != nil || retry.ID != root.ID || !retry.PublishedAt.Equal(root.PublishedAt) || !retry.ExpiresAt.Equal(root.ExpiresAt) {
				t.Fatal("renamed key lost conversion authority or changed clocks", err)
			}
			parent, err := s.GetDeploymentRegistryVerificationByID(t.Context(), app.AccountID, app.ID, in.DeploymentID, root.Input.RegistryVerificationID)
			if err != nil {
				t.Fatal(err)
			}
			renewed := cloneRegistryVerificationInput(parent.Input)
			renewed.ID = uuid.NewString()
			approval, err := s.RecordDeploymentRegistryVerification(t.Context(), renewed)
			if err != nil {
				t.Fatal("renamed key lost registry renewal authority", err)
			}
			assertRegistryPublisherCryptography(t, s, renewed)
			in.RegistryVerificationID, in.RegistryInputHash = approval.ID, approval.InputHash
			if _, err := s.PublishDeploymentArtifactScan(t.Context(), in); err != nil {
				t.Fatal("renamed key lost scan publication authority", err)
			}
			if err := s.DeleteAppTrustedSigner(t.Context(), app.AccountID, app.ID, alias); err != nil {
				t.Fatal(err)
			}
			installForeignPublisher(t, s, app, der)
			in.ID = uuid.NewString()
			if _, err := s.PublishDeploymentArtifactScan(t.Context(), in); !errors.Is(err, imagepublisher.ErrSignatureInvalid) {
				t.Fatal("foreign approval substituted for revoked registry key", err)
			}
		})
	}
}

func assertRegistryPublisherCryptography(t *testing.T, s registryVerificationTestStore, in DeploymentRegistryVerificationInput) {
	t.Helper()
	forged := cloneRegistryVerificationInput(in)
	forged.ID = uuid.NewString()
	forged.Proof.Evidence.Signature[len(forged.Proof.Evidence.Signature)-1] ^= 1
	forged.Proof.SignatureDigest = fmt.Sprintf("sha256:%x", sha256.Sum256(forged.Proof.Evidence.Signature))
	if _, _, err := prepareRegistryVerification(forged); err != nil {
		t.Fatal("forged fixture did not reach signature authentication", err)
	}
	if _, err := s.RecordDeploymentRegistryVerification(t.Context(), forged); !errors.Is(err, imagepublisher.ErrSignatureInvalid) {
		t.Fatal("approved fingerprint admitted a forged registry signature", err)
	}
}
