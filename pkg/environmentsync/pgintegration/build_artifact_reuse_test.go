package pgintegration_test

import (
	"bytes"
	"compress/gzip"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/json"
	"errors"
	"io"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onebox-faas/faas/migrations"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/buildpublisher"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/environmentsync"
	"github.com/onebox-faas/faas/pkg/imagechain"
	"github.com/onebox-faas/faas/pkg/state"
)

// adr: 20261017190804, 1500 — shared runner claims and borrowed identity survive migration ordering.
// Signed byte fixtures prove state and SQL contracts, not native ext4 mounting.
func TestGitOpsBuildArtifactReuseKeepsTargetConfigAndNeverQueuesBuild(t *testing.T) {
	for _, kind := range []string{"source", "source-node24", "dockerfile", "function"} {
		t.Run(kind, func(t *testing.T) {
			for _, targetEnvironment := range []string{"staging", "production"} {
				t.Run(targetEnvironment, func(t *testing.T) {
					buildArtifactStores(t, func(t *testing.T, basic gitOpsTestStore, pool *pgxpool.Pool) {
						ctx := t.Context()
						fixtureKind, claimedRuntime := kind, ""
						if kind == "source-node24" {
							fixtureKind, claimedRuntime = "source", "node24"
						}
						store, originLease, app, _, plan := appliedSourceCandidateFixture(t, basic, fixtureKind)
						prepare := basic.(state.EnvironmentGitOpsPreparationStore)
						requests, err := prepare.EnvironmentGitOpsSourceRequests(ctx, originLease, plan)
						if err != nil || len(requests) != 1 {
							t.Fatal("source request", err)
						}
						archive := sourceArtifact(t, requests[0])
						candidates, err := prepare.PrepareEnvironmentGitOpsCandidates(ctx, originLease, plan, map[string]state.EnvironmentWorkloadSourceArtifact{"workload/api": archive})
						if err != nil || len(candidates) != 1 {
							t.Fatal("origin candidate", err)
						}
						origin, err := store.DeploymentByID(ctx, candidates[0].DeploymentID)
						if err != nil {
							t.Fatal(err)
						}
						root, publication := publishReuseSourceArtifact(t, basic, origin, claimedRuntime)
						origin, err = store.DeploymentByID(ctx, origin.ID)
						if err != nil {
							t.Fatal(err)
						}
						if graph, err := basic.(state.EnvironmentGitOpsGraphPreparationStore).ReconcileEnvironmentGitOpsPreparation(ctx, originLease, plan); err != nil || graph.Phase != "prepared" || graph.PreparedAt == nil {
							t.Fatal("origin preparation", graph.Phase, err)
						}
						// The origin remains held; artifact reuse grants no source serving claim.
						spec := originLease.Source.Spec
						spec.ManifestPath = "staging.yaml"
						targetSource := originLease.Source
						if targetEnvironment != originLease.Source.EnvironmentSlug {
							targetSource, err = basic.CreateEnvironmentGitSource(ctx, app.AccountID, app.ProjectID, targetEnvironment, spec)
							if err != nil {
								t.Fatal(err)
							}
						}
						var definition api.EnvironmentDefinition
						if json.Unmarshal(originLease.Revision.Definition, &definition) != nil {
							t.Fatal("definition")
						}
						definition.Environment = targetEnvironment
						w := definition.Workloads["api"]
						w.App = app.Slug
						w.Source.BuildArtifactID, w.Source.BuildArtifactHash = root.ID, root.InputHash
						var targetRuntime map[string]json.RawMessage
						if err := json.Unmarshal(w.Runtime, &targetRuntime); err != nil {
							t.Fatal(err)
						}
						targetRuntime["port"] = json.RawMessage(`9080`)
						w.Runtime, err = json.Marshal(targetRuntime)
						if err != nil {
							t.Fatal(err)
						}
						w.Variables = map[string]string{"MODE": "TARGET_PRIVATE"}
						definition.Workloads["api"] = w
						desired, err := environmentsync.Compile(definition)
						if err != nil {
							t.Fatal(err)
						}
						// Unknown producers and altered hashes cannot pin or advance approval.
						for _, bad := range []struct{ id, hash string }{{uuid.NewString(), root.InputHash}, {root.ID, strings.Repeat("0", 64)}} {
							raw, _ := json.Marshal(desired.Definition)
							var invalid api.EnvironmentDefinition
							_ = json.Unmarshal(raw, &invalid)
							iw := invalid.Workloads["api"]
							iw.Source.BuildArtifactID, iw.Source.BuildArtifactHash = bad.id, bad.hash
							invalid.Workloads["api"] = iw
							badDesired, err := environmentsync.Compile(invalid)
							if err != nil {
								t.Fatal(err)
							}
							if _, _, err = basic.ApproveEnvironmentDesiredRevision(ctx, approval(targetSource, badDesired, strings.Repeat("e", 40))); err == nil {
								t.Fatal("unknown or altered producer approved")
							}
						}
						targetSource, _, err = basic.ApproveEnvironmentDesiredRevision(ctx, approval(targetSource, desired, strings.Repeat("e", 40)))
						if err != nil {
							t.Fatal("pin approval", err)
						}
						adoptWorkloadIntent(t, store, targetSource)
						lease, err := basic.ClaimEnvironmentGitOps(ctx, "artifact-target", time.Now(), 3*time.Minute)
						if err != nil || lease.Source.ID != targetSource.ID {
							t.Fatal("target lease", err)
						}
						if _, err = store.ApplyEnvironmentGitOps(ctx, lease, claimedIntentPlan(t, store, lease, desired)); err != nil {
							t.Fatal("target intent", err)
						}
						targetPlan := claimedIntentPlan(t, store, lease, desired)
						before, err := store.ListBuildsForAccount(ctx, app.AccountID)
						if err != nil {
							t.Fatal(err)
						}
						requests, err = prepare.EnvironmentGitOpsSourceRequests(ctx, lease, targetPlan)
						if err != nil || len(requests) != 0 {
							t.Fatal("reuse requested a Git archive", err)
						}
						candidates, err = prepare.PrepareEnvironmentGitOpsCandidates(ctx, lease, targetPlan, nil)
						if err != nil || len(candidates) != 1 {
							t.Fatal("reused candidate", err)
						}
						target, err := store.DeploymentByID(ctx, candidates[0].DeploymentID)
						if err != nil {
							t.Fatal(err)
						}
						frozen, err := target.ScopedWorkloadRuntime()
						if err != nil || frozen.BuiltArtifact == nil || frozen.BuiltArtifact.ID != root.ID || frozen.BuiltArtifact.Digest != root.Input.ArtifactDigest || frozen.BuiltArtifact.CommitSHA != origin.CommitSHA || target.RootfsKey != root.Input.StorageKey || target.BuildID != "" || !target.EnvironmentWorkloadHeld() || target.Status != state.DeploySnapshotting || frozen.Variables["MODE"] != "TARGET_PRIVATE" || string(frozen.Runtime["port"]) != "9080" || frozen.SourceArchive != nil {
							t.Fatalf("borrowed identity/config: %+v %v", frozen, err)
						}
						after, err := store.ListBuildsForAccount(ctx, app.AccountID)
						if err != nil || len(after) != len(before) {
							t.Fatal("reuse enqueued a build", err)
						}
						again, err := prepare.PrepareEnvironmentGitOpsCandidates(ctx, lease, targetPlan, nil)
						if err != nil || len(again) != 1 || again[0].DeploymentID != target.ID {
							t.Fatal("retry duplicated candidate", err)
						}
						if graph, err := basic.(state.EnvironmentGitOpsGraphPreparationStore).ReconcileEnvironmentGitOpsPreparation(ctx, lease, targetPlan); err != nil || graph.Phase != "prepared" || graph.PreparedAt == nil {
							t.Fatal("borrowed candidate did not reach target preparation", graph.Phase, err)
						}
						inputs, err := basic.(state.DeploymentRuntimeProducerInputStore).GetFreshDeploymentRuntimeProducerInputs(ctx, app.AccountID, app.ID, target.ID)
						if err != nil || inputs.DeploymentID != uuid.MustParse(target.ID).String() || inputs.Scope != targetEnvironment {
							t.Fatal("borrower lost target identity", err)
						}
						if _, err := store.LatestSnapshot(ctx, target.ID); !errors.Is(err, state.ErrNotFound) {
							t.Fatal("source snapshot or qualification copied", err)
						}
						if pool != nil {
							var raw []byte
							if err := pool.QueryRow(ctx, `SELECT application_standard_runtime_producers(a,d) FROM apps a JOIN deployments d ON d.app_id=a.id WHERE d.id=$1`, target.ID).Scan(&raw); err != nil {
								t.Fatal("SQL borrower projection", err)
							}
							var projection struct {
								DeploymentID string                            `json:"deployment_id"`
								Scope        string                            `json:"scope"`
								Artifacts    []state.DeploymentRuntimeArtifact `json:"artifacts"`
							}
							if json.Unmarshal(raw, &projection) != nil || projection.DeploymentID != target.ID || projection.Scope != target.Scope || !reflect.DeepEqual(projection.Artifacts, inputs.Artifacts) {
								t.Fatal("SQL and Go producer identity disagree")
							}
							var deadline time.Time
							if err := pool.QueryRow(ctx, `SELECT application_standard_native_source_producer_deadline(jsonb_build_object('account_id',a.account_id::text,'org_id',coalesce(a.org_id::text,''),'app_id',a.id::text,'artifact',jsonb_build_object('id',d.id::text,'scope',d.scope)),application_standard_source_runtime_producer(a,d),clock_timestamp()) FROM apps a JOIN deployments d ON d.app_id=a.id WHERE d.id=$1`, target.ID).Scan(&deadline); err != nil || !deadline.Equal(publication.ExpiresAt) {
								t.Fatal("native publisher deadline lost origin scope", err)
							}
							// Approval and candidates retain the same producer through ledger replay.
							for _, name := range []string{
								"20261017072644435_environment_built_artifact_reuse.sql",
								"20261017161625827_gitops_borrowed_shared_runtime.sql",
								"20261017161625827_gitops_borrowed_shared_runtime.sql",
							} {
								migration, err := migrations.FS.ReadFile(name)
								if err != nil {
									t.Fatal(err)
								}
								up := strings.Split(string(migration), "-- +goose Down")[0]
								if _, err = pool.Exec(ctx, up); err != nil {
									t.Fatal("migration replay", name, err)
								}
							}
							if err := pool.QueryRow(ctx, `SELECT application_standard_runtime_producers(a,d) FROM apps a JOIN deployments d ON d.app_id=a.id WHERE d.id=$1`, target.ID).Scan(&raw); err != nil {
								t.Fatal("SQL borrower projection after replay", err)
							}
							if json.Unmarshal(raw, &projection) != nil || projection.DeploymentID != target.ID || projection.Scope != target.Scope || !reflect.DeepEqual(projection.Artifacts, inputs.Artifacts) {
								t.Fatal("replay changed SQL and Go producer identity")
							}
							var pins int
							if err = pool.QueryRow(ctx, `SELECT count(*) FROM environment_build_artifact_pins WHERE artifact_id=$1`, root.ID).Scan(&pins); err != nil || pins != 2 {
								t.Fatal("replay removed retained pins", pins, err)
							}
							// A racing approval writer must be excluded from a GC key lock.
							tx, err := pool.Begin(ctx)
							if err != nil {
								t.Fatal(err)
							}
							defer func() { _ = tx.Rollback(ctx) }()
							if _, err = tx.Exec(ctx, `SELECT 1 FROM layer_artifact_retention WHERE storage_key=$1 FOR UPDATE`, root.Input.StorageKey); err != nil {
								t.Fatal(err)
							}
							racing, err := pool.Begin(ctx)
							if err != nil {
								t.Fatal(err)
							}
							defer func() { _ = racing.Rollback(ctx) }()
							if _, err = racing.Exec(ctx, `SET LOCAL lock_timeout='100ms'`); err != nil {
								t.Fatal(err)
							}
							if _, err = racing.Exec(ctx, `SELECT environment_pin_build_artifact($1,$2,$3,$4)`, root.ID, root.InputHash, app.AccountID, app.ProjectID); err == nil {
								t.Fatal("approval bypassed GC key fence")
							} else {
								var pgErr *pgconn.PgError
								if !errors.As(err, &pgErr) || pgErr.Code != "55P03" {
									t.Fatal("unexpected GC fence error", err)
								}
							}
							_ = racing.Rollback(ctx)
							_ = tx.Rollback(ctx)
						}
						if _, claimed, err := basic.(state.LayerArtifactRetentionStore).ClaimLayerArtifactDeletion(ctx, root.Input.StorageKey); err != nil || claimed {
							t.Fatal("pinned bytes collectible", err)
						}
						if err = store.SetDeploymentRootfs(ctx, origin.ID, "/changed.ext4", "changed.ext4", 1); err == nil {
							t.Fatal("pinned origin metadata changed")
						}
						if err = store.DeleteAppTrustedSigner(ctx, app.AccountID, app.ID, "company"); err != nil {
							t.Fatal(err)
						}
						if _, err = basic.(state.DeploymentRuntimeProducerInputStore).GetFreshDeploymentRuntimeProducerInputs(ctx, app.AccountID, app.ID, target.ID); err == nil {
							t.Fatal("revoked publisher retained target authority")
						}
						// Detach releases manifest pins, while candidate history still prevents
						// origin cleanup. Explicit app retirement remains possible afterwards.
						current, err := basic.EnvironmentGitSource(ctx, app.AccountID, app.ProjectID, targetEnvironment)
						if err != nil {
							t.Fatal(err)
						}
						prune := false
						current, err = basic.(state.EnvironmentGitOpsControlStore).UpdateEnvironmentGitSource(ctx, app.AccountID, current.ID, api.EnvironmentGitSourceUpdate{ExpectedGeneration: current.Generation, Mode: "report", Prune: &prune})
						if err != nil {
							t.Fatal("switch retired source to report", err)
						}
						if err = basic.(state.EnvironmentGitOpsLifecycleStore).DetachEnvironmentGitSource(ctx, app.AccountID, current.ID, current.Generation); err != nil {
							t.Fatal(err)
						}
						if err = store.ClearDeployment(ctx, origin.ID, "test"); err == nil {
							t.Fatal("detaching released candidate provenance")
						}
						if pool != nil {
							var pins int
							if err = pool.QueryRow(ctx, `SELECT count(*) FROM environment_build_artifact_pins WHERE artifact_id=$1`, root.ID).Scan(&pins); err != nil || pins != 1 {
								t.Fatal("detach lost candidate pin", pins, err)
							}
						}
						if targetEnvironment != originLease.Source.EnvironmentSlug {
							current, err = basic.EnvironmentGitSource(ctx, app.AccountID, app.ProjectID, "production")
							if err != nil {
								t.Fatal(err)
							}
							current, err = basic.(state.EnvironmentGitOpsControlStore).UpdateEnvironmentGitSource(ctx, app.AccountID, current.ID, api.EnvironmentGitSourceUpdate{ExpectedGeneration: current.Generation, Mode: "report", Prune: &prune})
							if err != nil {
								t.Fatal("switch origin to report", err)
							}
							if err = basic.(state.EnvironmentGitOpsLifecycleStore).DetachEnvironmentGitSource(ctx, app.AccountID, current.ID, current.Generation); err != nil {
								t.Fatal(err)
							}
						}

						if _, err = store.SoftDeleteAppCascade(ctx, app.ID); err != nil {
							t.Fatal("candidate history prevented explicit owner retirement", err)
						}
						if _, claimed, err := basic.(state.LayerArtifactRetentionStore).ClaimLayerArtifactDeletion(ctx, root.Input.StorageKey); err != nil || claimed {
							t.Fatal("owner tombstone discarded retained candidate bytes", err)
						}
					})
				})
			}
		})
	}
}

func reuseBaseProducerFixture(t *testing.T, key string, plain ...string) state.BaseImageProducerInput {
	t.Helper()
	var descriptors []imagechain.Descriptor
	var diffs []string
	var consumed []imagechain.LayerConsumption
	for i, value := range plain {
		var compressed bytes.Buffer
		z := gzip.NewWriter(&compressed)
		if _, err := z.Write([]byte(value)); err != nil {
			t.Fatal(err)
		}
		if err := z.Close(); err != nil {
			t.Fatal(err)
		}
		d := imagechain.Descriptor{Digest: imagechain.Digest(compressed.Bytes()), Size: int64(compressed.Len())}
		diff := imagechain.Digest([]byte(value))
		stream, err := imagechain.NewLayerStream(t.Context(), io.NopCloser(bytes.NewReader(compressed.Bytes())), d, diff, i)
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
		c, err := stream.Consumption()
		if err != nil {
			t.Fatal(err)
		}
		if err := stream.Close(); err != nil {
			t.Fatal(err)
		}
		descriptors = append(descriptors, d)
		diffs = append(diffs, diff)
		consumed = append(consumed, c)
	}
	config, _ := json.Marshal(map[string]any{"os": "linux", "architecture": "amd64", "rootfs": map[string]any{"type": "layers", "diff_ids": diffs}})
	manifest, _ := json.Marshal(map[string]any{"schemaVersion": 2, "mediaType": "application/vnd.oci.image.manifest.v1+json", "config": imagechain.Descriptor{Digest: imagechain.Digest(config), Size: int64(len(config))}, "layers": descriptors})
	digest := imagechain.Digest(manifest)
	artifact := []byte("produced complete artifact " + key)
	return state.BaseImageProducerInput{ID: uuid.NewString(), Artifact: imagechain.BaseArtifact{StorageKey: key, Digest: imagechain.Digest(artifact), Bytes: int64(len(artifact))}, SourceReference: "registry.example/base@" + digest, SourceDigest: digest, SelectedDigest: digest, ImageChain: &imagechain.Evidence{SourceManifest: manifest, Config: config}, LayoutVersion: imagechain.BaseLayoutVersion, GuestInitDigest: imagechain.Digest([]byte("init")), ContentBytes: 8, Layers: consumed}
}

func buildArtifactStores(t *testing.T, run func(*testing.T, gitOpsTestStore, *pgxpool.Pool)) {
	t.Helper()
	t.Run("memory", func(t *testing.T) { run(t, state.NewMemStore(), nil) })
	t.Run("postgres", func(t *testing.T) {
		if os.Getenv("GREGALE_GITOPS_ACCEPTANCE") == "1" && (os.Getenv("DATABASE_URL") == "" || os.Getenv("FAAS_SKIP_PG_TESTS") != "") {
			t.Fatal("GitOps acceptance requires PostgreSQL")
		}
		pool := pgtest.OpenMigrated(t)
		if err := db.MigrateUp(t.Context(), pool); err != nil {
			t.Fatal(err)
		}
		run(t, state.NewPgStore(pool), pool)
	})
}

func publishReuseSourceArtifact(t *testing.T, basic gitOpsTestStore, origin state.Deployment, claimedRuntime ...string) (state.SourceBuildRootfs, state.BuildExportPublication) {
	t.Helper()
	ctx, store := t.Context(), basic
	app, err := store.AppByID(ctx, origin.AppID)
	if err != nil {
		t.Fatal(err)
	}
	build, err := store.ClaimQueuedBuild(ctx, origin.BuildID)
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
	if _, _, err = store.UpsertAppTrustedSigner(ctx, app.AccountID, app.ID, "company", der, app.AccountID); err != nil {
		t.Fatal(err)
	}
	runtime := app.Runtime
	if len(claimedRuntime) != 0 && claimedRuntime[0] != "" {
		runtime = claimedRuntime[0]
	}
	claims := buildpublisher.Claims{Format: buildpublisher.Format, AccountID: uuid.MustParse(app.AccountID).String(), OrgID: app.OrgID, AppID: uuid.MustParse(app.ID).String(), DeploymentID: uuid.MustParse(origin.ID).String(), BuildID: uuid.MustParse(build.ID).String(), ClaimStartedAt: build.StartedAt.UTC().Format(time.RFC3339Nano), SourceSHA256: origin.SourceSHA256, ExportDigest: imagechain.Digest([]byte("export")), ExportBytes: 6, Runtime: runtime, BuilderNodeID: "builder-one"}
	proof, err := buildpublisher.Sign(ctx, claims, "company", key)
	if err != nil {
		t.Fatal(err)
	}
	publication, err := basic.(state.BuildExportPublicationStore).RecordBuildExportPublication(ctx, state.BuildExportPublicationInput{ID: uuid.NewString(), Claims: claims, Proof: proof})
	if err != nil {
		t.Fatal("signed export", err)
	}
	if err = store.CompleteBuild(ctx, build, "/builder/export.tar", "apps/source/export.ext4", 6, state.BuildProvenance{BuildID: build.ID, SourceSHA256: origin.SourceSHA256, Plan: string(api.PlanPro), BuilderNodeID: "builder-one", StartedAt: build.StartedAt, FinishedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	origin, err = store.DeploymentByID(ctx, origin.ID)
	if err != nil {
		t.Fatal(err)
	}
	base, err := basic.(state.BaseImageProducerStore).PublishBaseImageProducer(ctx, reuseBaseProducerFixture(t, state.RuntimeBaseKeyForArch(runtime, "amd64"), "base"))
	if err != nil {
		t.Fatal("base", err)
	}
	intentHash, err := state.SourceBuildRootfsIntentHash(app, origin)
	if err != nil {
		t.Fatal(err)
	}
	rootKind, runner := state.SourceBuildRootfsKindForClaim(app, origin, runtime)
	rootInput := state.SourceBuildRootfsInput{ID: uuid.NewString(), PublicationID: publication.ID, PublicationHash: publication.InputHash, AccountID: app.AccountID, OrgID: app.OrgID, AppID: app.ID, DeploymentID: origin.ID, Scope: origin.Scope, Kind: rootKind, Runtime: runner, IntentHash: intentHash, StorageKey: "apps/source/converted.ext4", RootfsPath: "/srv/apps/source/converted.ext4", ContentBytes: 24, ArtifactDigest: imagechain.Digest([]byte("converted ext4")), ArtifactBytes: 24, BaseProducerID: base.ID, BaseInputHash: base.InputHash, GuestInitDigest: base.Input.GuestInitDigest, LayoutVersion: state.SourceBuildRootfsLayout}
	if rootKind == "function-layer" {
		rootInput.RunnerDigest = imagechain.Digest([]byte("runner"))
	}
	root, err := basic.(state.SourceBuildRootfsStore).PublishSourceBuildRootfs(ctx, rootInput)
	if err != nil {
		t.Fatal("root producer", err)
	}
	// imaged publishes the producer before handing a complete layer to
	// held snapshot/qualification preparation; no VM snapshot is fabricated.
	if err = store.UpdateDeploymentStatus(ctx, origin.ID, state.DeploySnapshotting, ""); err != nil {
		t.Fatal("complete origin imaging", err)
	}
	return root, publication
}
