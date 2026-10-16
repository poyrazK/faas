package state

// adr: 435. Signed export fixtures do not prove a converted/native runtime.

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"errors"
	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/buildpublisher"
	"strings"
	"testing"
	"time"
)

type buildExportTestStore interface {
	Store
	BuildExportPublicationStore
}

func buildExportFixture(t *testing.T, s buildExportTestStore) (BuildExportPublicationInput, App, Deployment, Build, *ecdsa.PrivateKey) {
	return buildExportFixtureRuntime(t, s, "node22")
}

func buildExportFixtureRuntime(t *testing.T, s buildExportTestStore, runtime string) (BuildExportPublicationInput, App, Deployment, Build, *ecdsa.PrivateKey) {
	t.Helper()
	owner, err := s.CreateAccountWithPersonalOrg(t.Context(), CreateAccountWithPersonalOrgParams{Email: uuid.NewString() + "@example.com", Plan: api.PlanPro})
	if err != nil {
		t.Fatal(err)
	}
	app, err := s.CreateApp(t.Context(), App{AccountID: owner.Account.ID, OrgID: owner.PersonalOrg.ID, Slug: "export-" + uuid.NewString()[:8], Runtime: runtime, RAMMB: 128, MaxConcurrency: 2})
	if err != nil {
		t.Fatal(err)
	}
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
	c := buildpublisher.Claims{Format: buildpublisher.Format, AccountID: canonicalStandardUUID(app.AccountID), OrgID: registryCanonicalOrg(app.OrgID), AppID: canonicalStandardUUID(app.ID), DeploymentID: canonicalStandardUUID(dep.ID), BuildID: canonicalStandardUUID(b.ID), ClaimStartedAt: b.StartedAt.UTC().Format(time.RFC3339Nano), SourceSHA256: dep.SourceSHA256, ExportDigest: "sha256:" + strings.Repeat("b", 64), ExportBytes: 42, Runtime: app.Runtime, BuilderNodeID: "builder-one"}
	proof, err := buildpublisher.Sign(t.Context(), c, "company", key)
	if err != nil {
		t.Fatal(err)
	}
	return BuildExportPublicationInput{ID: uuid.NewString(), Claims: c, Proof: proof}, app, dep, b, key
}

func completeBuildExportFixture(t *testing.T, s buildExportTestStore, in BuildExportPublicationInput, b Build) {
	t.Helper()
	p := BuildProvenance{BuildID: b.ID, SourceSHA256: in.Claims.SourceSHA256, Plan: string(api.PlanPro), BuilderNodeID: in.Claims.BuilderNodeID, StartedAt: b.StartedAt, FinishedAt: time.Now()}
	if err := s.CompleteBuild(t.Context(), b, "/builder/image.tar", "apps/export/app-layer.ext4", in.Claims.ExportBytes, p); err != nil {
		t.Fatal(err)
	}
}

func buildExportLifecycle(t *testing.T, s buildExportTestStore) {
	in, app, dep, b, _ := buildExportFixture(t, s)
	if present, err := s.HasBuildExportPublication(t.Context(), app.AccountID, app.ID, dep.ID); err != nil || present {
		t.Fatal("unpublished export has retained history", present, err)
	}
	value, err := s.RecordBuildExportPublication(t.Context(), in)
	if err != nil {
		t.Fatal(err)
	}
	assertBuildExportHistory(t, s, app.AccountID, app.ID, dep.ID, true)
	assertBuildExportHistory(t, s, uuid.NewString(), app.ID, dep.ID, false)
	if _, err := s.GetFreshBuildExportPublication(t.Context(), app.AccountID, app.ID, dep.ID, b.ID); !errors.Is(err, ErrApplicationStandardRuntimeStale) {
		t.Fatal("uncompleted build approved", err)
	}
	completeBuildExportFixture(t, s, in, b)
	fresh, err := s.GetFreshBuildExportPublication(t.Context(), app.AccountID, app.ID, dep.ID, b.ID)
	if err != nil || fresh.InputHash != value.InputHash {
		t.Fatal("completed export unavailable", err)
	}
	fresh.Input.Proof.Payload[0] ^= 1
	retry, err := s.RecordBuildExportPublication(t.Context(), in)
	if err != nil || retry.VerifiedAt != value.VerifiedAt || retry.ExpiresAt != value.ExpiresAt {
		t.Fatal("immutable retry changed clock or aliased bytes", err)
	}
	if _, err := s.GetFreshBuildExportPublication(t.Context(), uuid.NewString(), app.ID, dep.ID, b.ID); !errors.Is(err, ErrNotFound) {
		t.Fatal("cross-account export exposed", err)
	}
	if err := s.DeleteAppTrustedSigner(t.Context(), app.AccountID, app.ID, "company"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetFreshBuildExportPublication(t.Context(), app.AccountID, app.ID, dep.ID, b.ID); !errors.Is(err, buildpublisher.ErrInvalid) {
		t.Fatal("revoked publisher approved", err)
	}
	if _, err := s.RecordBuildExportPublication(t.Context(), in); !errors.Is(err, buildpublisher.ErrInvalid) {
		t.Fatal("retry bypassed current publisher", err)
	}
	assertBuildExportHistory(t, s, app.AccountID, app.ID, dep.ID, true)
}

func assertBuildExportHistory(t *testing.T, s buildExportTestStore, accountID, appID, depID string, want bool) {
	t.Helper()
	if got, err := s.HasBuildExportPublication(t.Context(), accountID, appID, depID); err != nil || got != want {
		t.Fatal("scoped retained history", got, err, "want", want)
	}
}

func buildExportClaimSubstitution(t *testing.T, s buildExportTestStore) {
	in, _, _, _, key := buildExportFixture(t, s)
	if _, err := s.RecordBuildExportPublication(t.Context(), in); err != nil {
		t.Fatal(err)
	}
	changed := in
	changed.ID = uuid.NewString()
	changed.Claims.ExportDigest = "sha256:" + strings.Repeat("c", 64)
	changed.Proof, _ = buildpublisher.Sign(t.Context(), changed.Claims, "company", key)
	if _, err := s.RecordBuildExportPublication(t.Context(), changed); !errors.Is(err, ErrConflict) {
		t.Fatal("claim export replacement accepted", err)
	}
	changed = in
	changed.ID = uuid.NewString()
	changed.Claims.ClaimStartedAt = time.Now().Add(time.Hour).UTC().Format(time.RFC3339Nano)
	changed.Proof, _ = buildpublisher.Sign(t.Context(), changed.Claims, "company", key)
	if _, err := s.RecordBuildExportPublication(t.Context(), changed); !errors.Is(err, ErrApplicationStandardRuntimeStale) {
		t.Fatal("different build claim accepted", err)
	}
	changed = in
	changed.ID = uuid.NewString()
	changed.Claims.SourceSHA256 = strings.Repeat("d", 64)
	changed.Proof, _ = buildpublisher.Sign(t.Context(), changed.Claims, "company", key)
	if _, err := s.RecordBuildExportPublication(t.Context(), changed); !errors.Is(err, ErrApplicationStandardRuntimeStale) {
		t.Fatal("different intent source accepted", err)
	}
}

func TestMemBuildExportPublicationLifecycle(t *testing.T) { buildExportLifecycle(t, NewMemStore()) }
func TestMemBuildExportPublicationSubstitution(t *testing.T) {
	buildExportClaimSubstitution(t, NewMemStore())
}

func TestMemBuildExportPublicationExpiry(t *testing.T) {
	s := NewMemStore()
	in, app, dep, b, _ := buildExportFixture(t, s)
	value, err := s.RecordBuildExportPublication(t.Context(), in)
	if err != nil {
		t.Fatal(err)
	}
	completeBuildExportFixture(t, s, in, b)
	value.VerifiedAt = value.VerifiedAt.Add(-2 * api.BuildExportPublicationVerificationTTL)
	value.ExpiresAt = value.VerifiedAt.Add(api.BuildExportPublicationVerificationTTL)
	s.mu.Lock()
	s.buildExportPublications[value.ID] = value
	s.mu.Unlock()
	if _, err := s.GetFreshBuildExportPublication(t.Context(), app.AccountID, app.ID, dep.ID, b.ID); !errors.Is(err, ErrApplicationStandardRuntimeStale) {
		t.Fatal("expired publication approved", err)
	}
}

func buildExportKeyRotation(t *testing.T, s buildExportTestStore) {
	t.Helper()
	in, app, dep, b, _ := buildExportFixture(t, s)
	old, err := s.RecordBuildExportPublication(t.Context(), in)
	if err != nil {
		t.Fatal(err)
	}
	completeBuildExportFixture(t, s, in, b)
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
	if _, err := s.GetFreshBuildExportPublication(t.Context(), app.AccountID, app.ID, dep.ID, b.ID); !errors.Is(err, buildpublisher.ErrInvalid) {
		t.Fatal("previous publisher key remained approved", err)
	}
	if _, err := s.RecordBuildExportPublication(t.Context(), in); !errors.Is(err, buildpublisher.ErrInvalid) {
		t.Fatal("old proof retry bypassed key rotation", err)
	}
	in.ID = uuid.NewString()
	in.Proof, err = buildpublisher.Sign(t.Context(), in.Claims, "company", key)
	if err != nil {
		t.Fatal(err)
	}
	renewed, err := s.RecordBuildExportPublication(t.Context(), in)
	if err != nil || renewed.ID == old.ID || renewed.Input.Claims != old.Input.Claims || !renewed.VerifiedAt.After(old.VerifiedAt) {
		t.Fatal("current key could not renew exact completed export", err)
	}
	fresh, err := s.GetFreshBuildExportPublication(t.Context(), app.AccountID, app.ID, dep.ID, b.ID)
	if err != nil || fresh.ID != renewed.ID || fresh.Input.Proof.PublisherKeySHA256 == old.Input.Proof.PublisherKeySHA256 {
		t.Fatal("renewed export omitted current publisher identity", err)
	}
}

func buildExportOwnerErasure(t *testing.T, s buildExportTestStore) string {
	t.Helper()
	in, app, dep, b, _ := buildExportFixture(t, s)
	old, err := s.RecordBuildExportPublication(t.Context(), in)
	if err != nil {
		t.Fatal(err)
	}
	completeBuildExportFixture(t, s, in, b)
	if _, err := s.ScheduleAppDeletion(t.Context(), app.ID, time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetFreshBuildExportPublication(t.Context(), app.AccountID, app.ID, dep.ID, b.ID); !errors.Is(err, ErrApplicationStandardRuntimeStale) {
		t.Fatal("deleted app exposed current approval", err)
	}
	if _, err := s.RestoreApp(t.Context(), app.ID, api.MustLimitsFor(api.PlanPro)); err != nil {
		t.Fatal(err)
	}
	restored, err := s.GetFreshBuildExportPublication(t.Context(), app.AccountID, app.ID, dep.ID, b.ID)
	if err != nil || restored.ID != old.ID || !restored.VerifiedAt.Equal(old.VerifiedAt) || !restored.ExpiresAt.Equal(old.ExpiresAt) {
		t.Fatal("restore changed retained approval or verification clock", err)
	}
	if _, err := s.ScheduleAppDeletion(t.Context(), app.ID, time.Now().Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err := s.ClaimAppDeletion(t.Context(), app.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteAppPermanently(t.Context(), app.ID); err != nil {
		t.Fatal("approval blocked owner erasure", err)
	}
	if _, err := s.GetFreshBuildExportPublication(t.Context(), app.AccountID, app.ID, dep.ID, b.ID); !errors.Is(err, ErrNotFound) {
		t.Fatal("erased owner retained approval", err)
	}
	assertBuildExportHistory(t, s, app.AccountID, app.ID, dep.ID, false)
	return b.ID
}

func TestMemBuildExportPublicationKeyRotation(t *testing.T) {
	buildExportKeyRotation(t, NewMemStore())
}

func TestMemBuildExportPublicationOwnerErasure(t *testing.T) {
	s := NewMemStore()
	buildID := buildExportOwnerErasure(t, s)
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, value := range s.buildExportPublications {
		if sameStandardUUID(value.Input.Claims.BuildID, buildID) {
			t.Fatal("erased export bytes retained")
		}
	}
}
