package state

// adr: 435. Authentic approvals and exact lineage; output bytes are fixtures.

import (
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/buildpublisher"
	"github.com/onebox-faas/faas/pkg/imagechain"
)

type sourceRootfsTestStore interface {
	buildExportTestStore
	SourceBuildRootfsStore
	BaseImageProducerStore
	DeploymentRuntimeProducerPresenceStore
	DeploymentRuntimeProducerInputStore
}

type sourceRootfsFixture struct {
	Input  SourceBuildRootfsInput
	App    App
	Dep    Deployment
	Build  Build
	Parent BuildExportPublication
	Base   BaseImageProducer
}

func sourceBuildRootfsFixture(t *testing.T, s sourceRootfsTestStore) sourceRootfsFixture {
	return sourceBuildRootfsFixtureRuntime(t, s, "node22")
}

func sourceBuildRootfsFixtureRuntime(t *testing.T, s sourceRootfsTestStore, runtime string) sourceRootfsFixture {
	t.Helper()
	approval, app, dep, build, _ := buildExportFixtureRuntime(t, s, runtime)
	parent, err := s.RecordBuildExportPublication(t.Context(), approval)
	if err != nil {
		t.Fatal(err)
	}
	completeBuildExportFixture(t, s, approval, build)
	dep, err = s.DeploymentByID(t.Context(), dep.ID)
	if err != nil {
		t.Fatal(err)
	}
	kind, runtime := SourceBuildRootfsKind(app, dep)
	base, err := s.PublishBaseImageProducer(t.Context(), baseProducerFixture(t, RuntimeBaseKeyForArch(runtime, "amd64"), "runtime base"))
	if err != nil {
		t.Fatal(err)
	}
	hash, err := SourceBuildRootfsIntentHash(app, dep)
	if err != nil {
		t.Fatal(err)
	}
	in := SourceBuildRootfsInput{ID: uuid.NewString(), PublicationID: parent.ID, PublicationHash: parent.InputHash,
		AccountID: app.AccountID, OrgID: app.OrgID, AppID: app.ID, DeploymentID: dep.ID, Scope: dep.Scope,
		Kind: kind, Runtime: runtime, IntentHash: hash, StorageKey: "apps/source/converted.ext4", RootfsPath: "/srv/apps/source/converted.ext4", ContentBytes: 24,
		ArtifactDigest: imagechain.Digest([]byte("converted ext4 fixture")), ArtifactBytes: 22, BaseProducerID: base.ID, BaseInputHash: base.InputHash,
		GuestInitDigest: base.Input.GuestInitDigest, RunnerDigest: imagechain.Digest([]byte("runner fixture")), LayoutVersion: SourceBuildRootfsLayout}
	if kind == "source-app-layer" {
		in.RunnerDigest = ""
	}
	return sourceRootfsFixture{Input: in, App: app, Dep: dep, Build: build, Parent: parent, Base: base}
}

func sourceBuildRootfsLifecycle(t *testing.T, s sourceRootfsTestStore) {
	t.Helper()
	f := sourceBuildRootfsFixture(t, s)
	value, err := s.PublishSourceBuildRootfs(t.Context(), f.Input)
	if err != nil {
		t.Fatal(err)
	}
	if value.PublishedAt.Before(f.Parent.VerifiedAt) || value.PublishedAt.Before(f.Base.PublishedAt) || !value.ExpiresAt.Equal(f.Parent.ExpiresAt) {
		t.Fatal("producer clock not bound to approval")
	}
	retry, err := s.PublishSourceBuildRootfs(t.Context(), f.Input)
	if err != nil || retry.InputHash != value.InputHash || !retry.PublishedAt.Equal(value.PublishedAt) || !retry.ExpiresAt.Equal(value.ExpiresAt) {
		t.Fatal("retry extended or changed evidence", err)
	}
	current, err := s.GetCurrentSourceBuildRootfs(t.Context(), f.App.AccountID, f.App.ID, f.Dep.ID)
	if err != nil || current.ID != value.ID {
		t.Fatal("selected producer missing", err)
	}
	dep, err := s.DeploymentByID(t.Context(), f.Dep.ID)
	if err != nil || !sourceBuildRootfsMetadataMatches(value, dep) {
		t.Fatal("metadata stamp not atomic", err)
	}
	if _, err := s.GetCurrentSourceBuildRootfs(t.Context(), uuid.NewString(), f.App.ID, f.Dep.ID); !errors.Is(err, ErrNotFound) {
		t.Fatal("cross-owner producer exposed", err)
	}
	if present, err := s.HasDeploymentRuntimeProducers(t.Context(), f.App.AccountID, f.App.ID, f.Dep.ID); err != nil || !present {
		t.Fatal("source history permits legacy fallback", present, err)
	}
	if inputs, err := s.GetFreshDeploymentRuntimeProducerInputs(t.Context(), f.App.AccountID, f.App.ID, f.Dep.ID); err != nil || len(inputs.Artifacts) != 2 || inputs.Artifacts[1].Kind != f.Input.Kind {
		t.Fatal("conversion cannot bootstrap distinct source scan inputs", err)
	}
	if err := s.DeleteAppTrustedSigner(t.Context(), f.App.AccountID, f.App.ID, "company"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.PublishSourceBuildRootfs(t.Context(), f.Input); !errors.Is(err, buildpublisher.ErrInvalid) {
		t.Fatal("retry bypassed revocation", err)
	}
	if old, err := s.GetCurrentSourceBuildRootfs(t.Context(), f.App.AccountID, f.App.ID, f.Dep.ID); err != nil || old.ID != value.ID {
		t.Fatal("history erased by revocation", err)
	}
}

func sourceBuildRootfsSuperseded(t *testing.T, s sourceRootfsTestStore) {
	t.Helper()
	f := sourceBuildRootfsFixture(t, s)
	first, err := s.PublishSourceBuildRootfs(t.Context(), f.Input)
	if err != nil {
		t.Fatal(err)
	}
	next := f.Input
	next.ID = uuid.NewString()
	second, err := s.PublishSourceBuildRootfs(t.Context(), next)
	if err != nil || second.ID == first.ID {
		t.Fatal("new conversion not selected", err)
	}
	if _, err := s.PublishSourceBuildRootfs(t.Context(), f.Input); !errors.Is(err, ErrConflict) {
		t.Fatal("old conversion reselected", err)
	}
	next.ArtifactDigest = imagechain.Digest([]byte("different output"))
	if _, err := s.PublishSourceBuildRootfs(t.Context(), next); !errors.Is(err, ErrConflict) {
		t.Fatal("same id rebound", err)
	}
	current, err := s.GetCurrentSourceBuildRootfs(t.Context(), f.App.AccountID, f.App.ID, f.Dep.ID)
	if err != nil || current.ID != second.ID {
		t.Fatal("refusal changed current selection", err)
	}
	replacement := f.Base.Input
	replacement.ID = uuid.NewString()
	if _, err := s.PublishBaseImageProducer(t.Context(), replacement); err != nil {
		t.Fatal(err)
	}
	next.ID = uuid.NewString()
	if _, err := s.PublishSourceBuildRootfs(t.Context(), next); !errors.Is(err, ErrApplicationStandardRuntimeStale) {
		t.Fatal("replaced base accepted", err)
	}
}

func sourceBuildRootfsLateIntent(t *testing.T, s sourceRootfsTestStore, mode string) {
	t.Helper()
	f := sourceBuildRootfsFixture(t, s)
	p := UpdateAppParams{}
	switch mode {
	case "command":
		command := "/new/start"
		p.StartCommand = &command
	case "manifest":
		manifest := f.App.Manifest
		manifest.Env = map[string]string{"CHANGED_STARTUP": "yes"}
		p.Manifest = &manifest
	case "require_signed":
		required := true
		p.RequireSigned = &required
		p.SetRequireSigned = true
	case "security_policy":
		policy := api.AppSecurityPolicyEnforce
		p.SecurityPolicy = &policy
		p.SetSecurityPolicy = true
	}
	if _, err := s.UpdateApp(t.Context(), f.App.ID, p); err != nil {
		t.Fatal(err)
	}
	if _, err := s.PublishSourceBuildRootfs(t.Context(), f.Input); !errors.Is(err, ErrApplicationStandardRuntimeStale) {
		t.Fatal("late intent accepted", err)
	}
	dep, err := s.DeploymentByID(t.Context(), f.Dep.ID)
	if err != nil || dep.RootfsPath != f.Dep.RootfsPath || dep.RootfsKey != f.Dep.RootfsKey || dep.RootfsBytes != f.Dep.RootfsBytes {
		t.Fatal("refusal changed deployment metadata", err)
	}
	if _, err := s.GetCurrentSourceBuildRootfs(t.Context(), f.App.AccountID, f.App.ID, f.Dep.ID); !errors.Is(err, ErrNotFound) {
		t.Fatal("refusal left selected evidence", err)
	}
}

func sourceBuildRootfsOwnerErasure(t *testing.T, s sourceRootfsTestStore) string {
	t.Helper()
	f := sourceBuildRootfsFixture(t, s)
	value, err := s.PublishSourceBuildRootfs(t.Context(), f.Input)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.ScheduleAppDeletion(t.Context(), f.App.ID, time.Now().Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err := s.ClaimAppDeletion(t.Context(), f.App.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteAppPermanently(t.Context(), f.App.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetCurrentSourceBuildRootfs(t.Context(), f.App.AccountID, f.App.ID, f.Dep.ID); !errors.Is(err, ErrNotFound) {
		t.Fatal("erased owner exposed evidence", err)
	}
	return value.ID
}

func TestMemSourceBuildRootfsLifecycle(t *testing.T)  { sourceBuildRootfsLifecycle(t, NewMemStore()) }
func TestMemSourceBuildRootfsSuperseded(t *testing.T) { sourceBuildRootfsSuperseded(t, NewMemStore()) }
func TestMemSourceBuildRootfsOwnerErasure(t *testing.T) {
	sourceBuildRootfsOwnerErasure(t, NewMemStore())
}

func sourceBuildRootfsSoftDeleteRestore(t *testing.T, s sourceRootfsTestStore) string {
	t.Helper()
	f := sourceBuildRootfsFixture(t, s)
	value, err := s.PublishSourceBuildRootfs(t.Context(), f.Input)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.ScheduleAppDeletion(t.Context(), f.App.ID, time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetCurrentSourceBuildRootfs(t.Context(), f.App.AccountID, f.App.ID, f.Dep.ID); !errors.Is(err, ErrNotFound) {
		t.Fatal("deleted owner exposed selected producer", err)
	}
	if _, err := s.RestoreApp(t.Context(), f.App.ID, api.MustLimitsFor(api.PlanPro)); err != nil {
		t.Fatal(err)
	}
	restored, err := s.GetCurrentSourceBuildRootfs(t.Context(), f.App.AccountID, f.App.ID, f.Dep.ID)
	if err != nil || restored.ID != value.ID || !restored.PublishedAt.Equal(value.PublishedAt) || !restored.ExpiresAt.Equal(value.ExpiresAt) {
		t.Fatal("restore replaced retained conversion or clocks", err)
	}
	return value.ID
}

func TestMemSourceBuildRootfsSoftDeleteRestore(t *testing.T) {
	sourceBuildRootfsSoftDeleteRestore(t, NewMemStore())
}
func TestMemSourceBuildRootfsLateIntent(t *testing.T) {
	for _, mode := range []string{"command", "manifest", "require_signed", "security_policy"} {
		t.Run(mode, func(t *testing.T) { sourceBuildRootfsLateIntent(t, NewMemStore(), mode) })
	}
}

func sourceBuildRootfsSubstitution(t *testing.T, s sourceRootfsTestStore, mode string) {
	t.Helper()
	f := sourceBuildRootfsFixture(t, s)
	in := f.Input
	switch mode {
	case "approval":
		in.PublicationHash = imagechain.Digest([]byte("another approval"))[7:]
	case "owner":
		in.AccountID = uuid.NewString()
	case "scope":
		in.Scope = "preview"
	case "base hash":
		in.BaseInputHash = imagechain.Digest([]byte("another base"))[7:]
	case "injected guest":
		in.GuestInitDigest = imagechain.Digest([]byte("different init"))
	case "missing runner":
		in.RunnerDigest = ""
	case "wrong kind":
		in.Kind = "source-app-layer"
		in.Runtime = ""
		in.RunnerDigest = ""
	case "new build":
		b, err := s.CreateBuild(t.Context(), f.Dep.ID, f.Dep.Kind, 1, "")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := s.ClaimQueuedBuild(t.Context(), b.ID); err != nil {
			t.Fatal(err)
		}
	case "queued build":
		if _, err := s.CreateBuild(t.Context(), f.Dep.ID, f.Dep.Kind, 1, ""); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.PublishSourceBuildRootfs(t.Context(), in); err == nil {
		t.Fatal("substituted input published")
	}
	dep, err := s.DeploymentByID(t.Context(), f.Dep.ID)
	if err != nil || dep.RootfsPath != f.Dep.RootfsPath || dep.RootfsBytes != f.Dep.RootfsBytes {
		t.Fatal("refusal partially stamped deployment", err)
	}
}

func TestMemSourceBuildRootfsSubstitution(t *testing.T) {
	for _, mode := range []string{"approval", "owner", "scope", "base hash", "injected guest", "missing runner", "wrong kind", "new build", "queued build"} {
		t.Run(mode, func(t *testing.T) { sourceBuildRootfsSubstitution(t, NewMemStore(), mode) })
	}
}

func TestSourceBuildRootfsIntentCanonicalization(t *testing.T) {
	app := App{Slug: "test", Manifest: AppManifest{Env: map[string]string{"A": "one", "B": "two"}}}
	dep := Deployment{Scope: "production", OverrideEnv: json.RawMessage(`{"C":"three","D":"four"}`)}
	first, err := SourceBuildRootfsIntentHash(app, dep)
	if err != nil {
		t.Fatal(err)
	}
	dep.OverrideEnv = json.RawMessage(`{"D": "four", "C": "three"}`)
	dep.OverrideMainDependsOn = json.RawMessage(`[]`)
	dep.OverrideCmd = []string{}
	second, err := SourceBuildRootfsIntentHash(app, dep)
	if err != nil || first != second {
		t.Fatal("equivalent JSONB inputs changed intent", err)
	}
	app.Manifest.Ports = []api.WorkloadPort{}
	changed, err := SourceBuildRootfsIntentHash(app, dep)
	if err != nil || changed == first {
		t.Fatal("explicit empty ports lost intent", err)
	}
}
