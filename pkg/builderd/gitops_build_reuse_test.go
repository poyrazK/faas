package builderd

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/environmentsync"
	"github.com/onebox-faas/faas/pkg/imagechain"
	"github.com/onebox-faas/faas/pkg/state"
)

// Real ECDSA approvals and state producer checks, with byte-only VM outputs.
// These tests do not claim native OCI conversion, VM or leak acceptance.
func TestGitOpsUnchangedBuildReuseAndTrustFallback(t *testing.T) {
	for _, tc := range []struct{ kind, scenario string }{
		{"source", "reuse"}, {"source", "revoked"}, {"source", "tampered export"}, {"source", "base replaced"}, {"source", "builder replaced"}, {"source", "recipe changed"},
		{"dockerfile", "reuse"}, {"function", "reuse"},
	} {
		t.Run(tc.kind+"/"+tc.scenario, func(t *testing.T) {
			scenario := tc.scenario
			store, origin, app := heldSourceBuild(t, tc.kind, true)
			t.Setenv("FAAS_DEPLOY_BASE_REF_NODE22", "registry.example/node@sha256:"+strings.Repeat("1", 64))
			signer, der := buildPublicationSigner(t)
			if _, _, err := store.UpsertAppTrustedSigner(t.Context(), app.AccountID, app.ID, "company", der, app.AccountID); err != nil {
				t.Fatal(err)
			}
			export := filepath.Join(t.TempDir(), "image.tar")
			if err := os.WriteFile(export, []byte("signed byte fixture"), 0o640); err != nil {
				t.Fatal(err)
			}
			vm := &fakeVM{out: BuildOutcome{OCIImage: export, BuildkitVer: "fixture-buildkit", RailpackVer: "fixture-railpack"}}
			cache := NewCache(t.TempDir())
			b := New(store, nil, vm, cache, NewDetector(), nil, Config{BuilderNodeID: "builder-one", SourceSpoolDir: filepath.Dir(origin.SourcePath)}, slog.New(slog.NewTextHandler(io.Discard, nil))).WithBuildPublisher(signer)
			if result, err := b.ProcessOne(t.Context(), origin.BuildID); err != nil || result.CacheHit {
				t.Fatalf("first build: %+v %v", result, err)
			}
			root := publishUnchangedOrigin(t, store, app, origin)
			target := nextUnchangedCandidate(t, store, app, origin, scenario == "recipe changed")
			b.cfg.SourceSpoolDir = filepath.Dir(target.SourcePath)
			if scenario == "revoked" {
				if err := store.DeleteAppTrustedSigner(t.Context(), app.AccountID, app.ID, "company"); err != nil {
					t.Fatal(err)
				}
				// The fallback is allowed to produce an unsigned build for this
				// non-requiring fixture. It must never reuse the revoked export.
			}
			if scenario == "tampered export" {
				effective, err := state.AppForDeploymentRuntime(app, origin)
				if err != nil {
					t.Fatal(err)
				}
				scope, _ := gitOpsReuseScope(effective, origin)
				recipe := testBuildCacheRecipe(origin.SourceSHA256, FrameworkNode, api.PlanPro, "registry.example/node@sha256:"+strings.Repeat("1", 64))
				recipe.SourceRoot = "src"
				recipe.GitOpsReuseScope = scope
				key, err := recipe.key()
				if err != nil {
					t.Fatal(err)
				}
				path := cache.entryPath(key, recipe.Framework, recipe.Plan)
				if err := os.WriteFile(path, []byte("tampered but locally rehashed"), 0o640); err != nil {
					t.Fatal(err)
				}
				digest, err := hashFile(path)
				if err != nil {
					t.Fatal(err)
				}
				if err := writeCacheSidecar(filepath.Join(filepath.Dir(path), "artifact.sha256"), digest); err != nil {
					t.Fatal(err)
				}
			}
			if scenario == "base replaced" {
				if _, err := store.PublishBaseImageProducer(t.Context(), unchangedBaseFixtureForRuntime(t, "new base", root.Input.Runtime)); err != nil {
					t.Fatal(err)
				}
			}
			if scenario == "builder replaced" {
				vm.environment = testBuildEnvironment
				vm.environment.BuilderBaseIdentity = "new builder"
			}
			result, err := b.ProcessOne(t.Context(), target.BuildID)
			if err != nil {
				t.Fatal(err)
			}
			wantHit := scenario == "reuse"
			if result.CacheHit != wantHit || vm.spawnCalls != map[bool]int{true: 1, false: 2}[wantHit] {
				t.Fatalf("reuse=%v spawn=%d want hit=%v", result.CacheHit, vm.spawnCalls, wantHit)
			}
			current, err := store.DeploymentByID(t.Context(), target.ID)
			if err != nil {
				t.Fatal(err)
			}
			frozen, err := current.ScopedWorkloadRuntime()
			if err != nil {
				t.Fatal(err)
			}
			if !current.EnvironmentWorkloadHeld() || current.Status == state.DeployLive || frozen.Variables["MODE"] != "target configuration" || frozen.BuiltArtifact != nil {
				t.Fatal("reuse bypassed target configuration/hold or manufactured a borrowed root")
			}
			provenance, err := store.BuildProvenanceByBuildID(t.Context(), target.BuildID)
			if err != nil || provenance.CommitSHA != strings.Repeat("b", 40) || provenance.SourceSHA256 != origin.SourceSHA256 {
				t.Fatalf("new commit provenance lost: %+v %v", provenance, err)
			}
			evidence, err := store.CurrentEnvironmentGitOpsWorkloadEvidence(t.Context(), app.AccountID, frozen.SourceID)
			if err != nil || evidence == nil || len(evidence.Workloads) != 1 {
				t.Fatalf("current cache progress unavailable: %+v %v", evidence, err)
			}
			progress := evidence.Workloads[0]
			if (progress.BuildCacheStatus == "hit") != wantHit || progress.Qualification == "complete" || progress.Serving == "complete" {
				t.Fatalf("cache progress lost the outcome or conferred qualification: %+v", progress)
			}
			if wantHit {
				proof, err := store.GetFreshBuildExportPublication(t.Context(), app.AccountID, app.ID, target.ID, target.BuildID)
				if err != nil || proof.Input.Claims.DeploymentID != uuid.MustParse(target.ID).String() {
					t.Fatal("cache hit did not publish its own current target proof", err)
				}
			}
		})
	}
}

func nextUnchangedCandidate(t *testing.T, store *state.MemStore, app state.App, origin state.Deployment, changeRecipe bool) state.Deployment {
	t.Helper()
	frozen, err := origin.ScopedWorkloadRuntime()
	if err != nil {
		t.Fatal(err)
	}
	source, err := store.EnvironmentGitSource(t.Context(), app.AccountID, app.ProjectID, "production")
	if err != nil {
		t.Fatal(err)
	}
	runtime := json.RawMessage(`{"port":8081}`)
	if changeRecipe {
		runtime = json.RawMessage(`{"port":8082}`)
	}
	desired, err := environmentsync.Compile(api.EnvironmentDefinition{APIVersion: environmentsync.APIVersion, Project: "frozen-build", Environment: "production", Workloads: map[string]api.EnvironmentWorkload{"api": {App: app.Slug, Source: frozen.Source, Runtime: runtime, Variables: map[string]string{"MODE": "target configuration"}}}})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err = store.ApproveEnvironmentDesiredRevision(t.Context(), state.ApproveEnvironmentRevision{AccountID: app.AccountID, SourceID: source.ID, ExpectedGeneration: source.Generation, CommitSHA: strings.Repeat("b", 40), Desired: desired, ApprovedBy: app.AccountID}); err != nil {
		t.Fatal(err)
	}
	lease, err := store.ClaimEnvironmentGitOps(t.Context(), "next-unchanged-build", time.Now(), time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	plan := func() environmentsync.Plan {
		observed, err := store.ObserveEnvironmentGitOps(t.Context(), lease, desired)
		if err != nil {
			t.Fatal(err)
		}
		plan, err := environmentsync.BuildPlan(desired, observed.State, observed.Owners, environmentsync.PlanOptions{Manager: source.ID, Revision: lease.Revision.ID, CommitSHA: lease.Revision.CommitSHA, Generation: lease.Source.Generation, Now: time.Now(), Overrides: observed.Overrides})
		if err != nil {
			t.Fatal(err)
		}
		return plan
	}
	if _, err := store.ApplyEnvironmentGitOps(t.Context(), lease, plan()); err != nil {
		t.Fatal(err)
	}
	reviewed := plan()
	requests, err := store.EnvironmentGitOpsSourceRequests(t.Context(), lease, reviewed)
	if err != nil || len(requests) != 1 {
		t.Fatal("new source request", err)
	}
	archive := *frozen.SourceArchive
	archive.BuildID, archive.CommitSHA, archive.RevisionID, archive.DefinitionDigest = requests[0].BuildID, requests[0].CommitSHA, requests[0].RevisionID, requests[0].DefinitionDigest
	archive.LogPath = filepath.Join(t.TempDir(), "build.log")
	candidates, err := store.PrepareEnvironmentGitOpsCandidates(t.Context(), lease, reviewed, map[string]state.EnvironmentWorkloadSourceArtifact{requests[0].Resource: archive})
	if err != nil || len(candidates) != 1 {
		t.Fatal("new candidate", err)
	}
	target, err := store.DeploymentByID(t.Context(), candidates[0].DeploymentID)
	if err != nil {
		t.Fatal(err)
	}
	return target
}

func publishUnchangedOrigin(t *testing.T, store *state.MemStore, app state.App, origin state.Deployment) state.SourceBuildRootfs {
	t.Helper()
	proof, err := store.GetFreshBuildExportPublication(t.Context(), app.AccountID, app.ID, origin.ID, origin.BuildID)
	if err != nil {
		t.Fatal(err)
	}
	effective, err := state.AppForDeploymentRuntime(app, origin)
	if err != nil {
		t.Fatal(err)
	}
	kind, runtime := state.SourceBuildRootfsKindForClaim(effective, origin, proof.Input.Claims.Runtime)
	base, err := store.PublishBaseImageProducer(t.Context(), unchangedBaseFixtureForRuntime(t, "base", runtime))
	if err != nil {
		t.Fatal(err)
	}
	hash, err := state.SourceBuildRootfsIntentHash(app, origin)
	if err != nil {
		t.Fatal(err)
	}
	input := state.SourceBuildRootfsInput{ID: uuid.NewString(), PublicationID: proof.ID, PublicationHash: proof.InputHash, AccountID: app.AccountID, OrgID: app.OrgID, AppID: app.ID, DeploymentID: origin.ID, Scope: origin.Scope, Kind: kind, Runtime: runtime, IntentHash: hash, StorageKey: "apps/source/converted.ext4", RootfsPath: "/srv/source/converted.ext4", ContentBytes: 24, ArtifactDigest: imagechain.Digest([]byte("converted fixture")), ArtifactBytes: 24, BaseProducerID: base.ID, BaseInputHash: base.InputHash, GuestInitDigest: base.Input.GuestInitDigest, LayoutVersion: state.SourceBuildRootfsLayout}
	if kind == "function-layer" {
		input.RunnerDigest = imagechain.Digest([]byte("runner"))
	}
	root, err := store.PublishSourceBuildRootfs(t.Context(), input)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.UpdateDeploymentStatus(t.Context(), origin.ID, state.DeploySnapshotting, ""); err != nil {
		t.Fatal(err)
	}
	return root
}

func unchangedBaseFixtureForRuntime(t *testing.T, label, runtime string) state.BaseImageProducerInput {
	t.Helper()
	var compressed bytes.Buffer
	z := gzip.NewWriter(&compressed)
	if _, err := z.Write([]byte(label)); err != nil {
		t.Fatal(err)
	}
	if err := z.Close(); err != nil {
		t.Fatal(err)
	}
	diff := imagechain.Digest([]byte(label))
	layer := imagechain.Descriptor{Digest: imagechain.Digest(compressed.Bytes()), Size: int64(compressed.Len())}
	stream, err := imagechain.NewLayerStream(t.Context(), io.NopCloser(bytes.NewReader(compressed.Bytes())), layer, diff, 0)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := gzip.NewReader(stream)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.Copy(io.Discard, stream.VerifyingUncompressedReader(decoded)); err != nil {
		t.Fatal(err)
	}
	if err := decoded.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := io.Copy(io.Discard, stream); err != nil {
		t.Fatal(err)
	}
	consumed, err := stream.Consumption()
	if err != nil {
		t.Fatal(err)
	}
	if err := stream.Close(); err != nil {
		t.Fatal(err)
	}
	config, _ := json.Marshal(map[string]any{"os": "linux", "architecture": "amd64", "rootfs": map[string]any{"type": "layers", "diff_ids": []string{diff}}})
	manifest, _ := json.Marshal(map[string]any{"schemaVersion": 2, "mediaType": "application/vnd.oci.image.manifest.v1+json", "config": imagechain.Descriptor{Digest: imagechain.Digest(config), Size: int64(len(config))}, "layers": []imagechain.Descriptor{layer}})
	digest := imagechain.Digest(manifest)
	return state.BaseImageProducerInput{ID: uuid.NewString(), Artifact: imagechain.BaseArtifact{StorageKey: state.RuntimeBaseKeyForArch(runtime, "amd64"), Digest: imagechain.Digest([]byte(label)), Bytes: int64(len(label))}, SourceReference: "registry.example/base@" + digest, SourceDigest: digest, SelectedDigest: digest, ImageChain: &imagechain.Evidence{SourceManifest: manifest, Config: config}, LayoutVersion: imagechain.BaseLayoutVersion, GuestInitDigest: imagechain.Digest([]byte("init")), ContentBytes: 8, Layers: []imagechain.LayerConsumption{consumed}}
}
