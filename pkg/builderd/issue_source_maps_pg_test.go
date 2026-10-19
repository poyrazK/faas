package builderd

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/issues"
	"github.com/onebox-faas/faas/pkg/state"
)

func seedSourceMapBuild(t *testing.T, ctx context.Context, st *state.PgStore, source, suffix string) (state.App, state.Deployment, state.Build) {
	t.Helper()
	account, err := st.CreateAccount(ctx, suffix+"@source-maps.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := st.CreateApp(ctx, state.App{AccountID: account.ID, Slug: "source-maps-" + suffix, Type: state.AppTypeApp, RAMMB: 256, IdleTimeoutS: 60, MaxConcurrency: 5})
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(source)
	if err != nil {
		t.Fatal(err)
	}
	dep, err := st.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Kind: state.DeploymentKindTarball, SourcePath: source, SourceBytes: info.Size(), LogPath: filepath.Join(t.TempDir(), "build.log")})
	if err != nil {
		t.Fatal(err)
	}
	build, err := st.CreateBuild(ctx, dep.ID, dep.Kind, info.Size(), dep.LogPath)
	if err != nil {
		t.Fatal(err)
	}
	return app, dep, build
}

func TestPgBuildSourceMapsAtomicCompletionAndFences(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	ctx := context.Background()
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatal(err)
	}
	st := state.NewPgStore(pool)
	source := sourceMapBuildArchive(t, "", `{"version":1,"directory":"/app/dist"}`)
	for _, scenario := range []string{"success", "wrong-owner", "stale", "cancelled", "conflict", "quota", "deferred-failure"} {
		t.Run(scenario, func(t *testing.T) {
			app, dep, build := seedSourceMapBuild(t, ctx, st, source, scenario)
			claim, err := st.ClaimQueuedBuild(ctx, build.ID)
			if err != nil {
				t.Fatal(err)
			}
			pub := state.BuildIssueSourceMapPublication{AppID: app.ID, AccountID: app.AccountID, Request: testBuildSourceMapRequest()}
			prov := state.BuildProvenance{BuildID: build.ID, SourceSHA256: strings.Repeat("a", 64), Plan: "pro", StartedAt: claim.StartedAt, FinishedAt: time.Now()}
			switch scenario {
			case "wrong-owner":
				pub.AccountID = "11111111-1111-4111-8111-111111111111"
			case "stale":
				claim.StartedAt = claim.StartedAt.Add(-time.Second)
			case "cancelled":
				if err := st.UpdateDeploymentStatus(ctx, dep.ID, state.DeployCancelled, ""); err != nil {
					t.Fatal(err)
				}
			case "conflict":
				other := testBuildSourceMapRequest()
				other.Manifest.Artifacts[0].SHA256 = strings.Repeat("c", 64)
				if _, err := st.PublishIssueSourceMaps(ctx, app.AccountID, app.ID, dep.ID, app.AccountID, other); err != nil {
					t.Fatal(err)
				}
			case "quota":
				for i := 0; i < api.IssueSourceMapDeploymentsMax; i++ {
					d, err := st.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Kind: dep.Kind})
					if err != nil {
						t.Fatal(err)
					}
					if _, err := st.PublishIssueSourceMaps(ctx, app.AccountID, app.ID, d.ID, app.AccountID, pub.Request); err != nil {
						t.Fatal(err)
					}
				}
			case "deferred-failure":
				_, err := pool.Exec(ctx, `CREATE FUNCTION reject_source_map_completion() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'injected commit failure'; END $$;
CREATE CONSTRAINT TRIGGER reject_source_map_completion AFTER INSERT ON build_provenance DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION reject_source_map_completion()`)
				if err != nil {
					t.Fatal(err)
				}
			}
			err = st.CompleteBuildWithIssueSourceMaps(ctx, claim, "/image.tar", "apps/test/app-layer.ext4", 42, prov, pub)
			got, readErr := st.BuildByID(ctx, build.ID)
			if readErr != nil {
				t.Fatal(readErr)
			}
			d, readErr := st.DeploymentByID(ctx, dep.ID)
			if readErr != nil {
				t.Fatal(readErr)
			}
			if scenario != "success" {
				if err == nil || got.Status == state.BuildSucceeded || d.RootfsPath != "" {
					t.Fatalf("partial build published: %+v %+v %v", got, d, err)
				}
				if _, err := st.BuildProvenanceByBuildID(ctx, build.ID); !errors.Is(err, state.ErrNotFound) {
					t.Fatal("partial provenance survived", err)
				}
				meta, err := st.GetIssueSourceMaps(ctx, app.AccountID, app.ID, dep.ID)
				if scenario == "conflict" {
					want := testBuildSourceMapRequest()
					want.Manifest.Artifacts[0].SHA256 = strings.Repeat("c", 64)
					set, compileErr := issues.CompileSourceMaps(want)
					if compileErr != nil || err != nil || meta.ManifestSHA256 != set.Digest {
						t.Fatal("immutable artifacts overwritten", err, compileErr)
					}
				} else if !errors.Is(err, state.ErrNotFound) {
					t.Fatal("partial source maps survived", err)
				}
				return
			}
			if err != nil || got.Status != state.BuildSucceeded || d.RootfsPath == "" {
				t.Fatal("atomic completion failed", err)
			}
			meta, err := st.GetIssueSourceMaps(ctx, app.AccountID, app.ID, dep.ID)
			if err != nil || !meta.Ready || meta.MapCount != 1 {
				t.Fatal("success preceded ready maps", err)
			}
			work, err := st.ListBuildsAwaitingImage(ctx, "", 16)
			if err != nil || len(work) != 1 || work[0].DeploymentID != dep.ID {
				t.Fatal("lost notification cannot recover", err)
			}
			// An ambiguous successful commit is fenced on retry. It must not
			// restore maps that an operator retired after the first completion.
			if _, err := st.RetireIssueSourceMaps(ctx, app.AccountID, app.ID, dep.ID, app.AccountID); err != nil {
				t.Fatal(err)
			}
			if err := st.CompleteBuildWithIssueSourceMaps(ctx, claim, "/other.tar", "other", 42, prov, pub); !errors.Is(err, state.ErrNotFound) {
				t.Fatal("completed claim replay mutated state", err)
			}
			meta, err = st.GetIssueSourceMaps(ctx, app.AccountID, app.ID, dep.ID)
			if err != nil || meta.Ready {
				t.Fatal("stale completion restored retired maps", err)
			}
		})
	}
}

func TestPgBuildCompletionWithoutSourceMaps(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	ctx := context.Background()
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatal(err)
	}
	st := state.NewPgStore(pool)
	source := sourceMapBuildArchive(t, "")
	app, dep, build := seedSourceMapBuild(t, ctx, st, source, "ordinary")
	claim, err := st.ClaimQueuedBuild(ctx, build.ID)
	if err != nil {
		t.Fatal(err)
	}
	prov := state.BuildProvenance{BuildID: build.ID, SourceSHA256: strings.Repeat("a", 64), Plan: "pro", StartedAt: claim.StartedAt, FinishedAt: time.Now()}
	if err := st.CompleteBuild(ctx, claim, "/image.tar", "apps/test/app-layer.ext4", 42, prov); err != nil {
		t.Fatal(err)
	}
	got, err := st.BuildByID(ctx, build.ID)
	if err != nil || got.Status != state.BuildSucceeded {
		t.Fatal("ordinary completion regressed", err)
	}
	if _, err := st.GetIssueSourceMaps(ctx, app.AccountID, app.ID, dep.ID); !errors.Is(err, state.ErrNotFound) {
		t.Fatal("ordinary build gained source maps", err)
	}
	if _, err := st.BuildProvenanceByBuildID(ctx, build.ID); err != nil {
		t.Fatal("ordinary provenance missing", err)
	}
	work, err := st.ListBuildsAwaitingImage(ctx, "", 16)
	if err != nil || len(work) != 1 || work[0].DeploymentID != dep.ID {
		t.Fatal("ordinary image recovery regressed", err)
	}
}

func TestPgBuildSourceMapsPublicationRetryAndCachedDeployment(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	ctx := context.Background()
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatal(err)
	}
	st := state.NewPgStore(pool)
	source := sourceMapBuildArchive(t, "", `{"version":1,"directory":"/app/dist"}`)
	app, dep, build := seedSourceMapBuild(t, ctx, st, source, "retry")
	image := filepath.Join(t.TempDir(), "image.tar")
	if err := os.WriteFile(image, []byte("image fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	vm := &fakeVM{out: BuildOutcome{OCIImage: image}}
	vm.waitHook = func() {
		request := vm.lastRequest
		digest, err := hashFile(image)
		if err != nil {
			t.Fatal(err)
		}
		handoff := api.BuildIssueSourceMaps{BuildID: request.BuildID, DeploymentID: request.DeploymentID, SourceSHA256: request.SourceSHA256, ImageSHA256: digest, Request: testBuildSourceMapRequest()}
		data, err := json.Marshal(handoff)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(filepath.Dir(image), api.BuildIssueSourceMapsFile), data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	// A sequence is not rolled back: first publication fails, the next
	// transaction succeeds without spawning a second VM or deployment.
	_, err := pool.Exec(ctx, `CREATE SEQUENCE source_map_write_attempt;
CREATE FUNCTION fail_first_source_map_write() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN
IF nextval('source_map_write_attempt')=1 THEN RAISE EXCEPTION 'injected publication interruption'; END IF; RETURN NEW; END $$;
CREATE TRIGGER fail_first_source_map_write BEFORE INSERT ON issue_source_map_artifacts FOR EACH ROW EXECUTE FUNCTION fail_first_source_map_write()`)
	if err != nil {
		t.Fatal(err)
	}
	boots := 0
	notifier := completionNotifier(func(ctx context.Context, channel, payload string) error {
		if channel != db.NotifySnapshotBoot {
			return nil
		}
		boots++
		var identity struct {
			DeploymentID string `json:"deployment_id"`
		}
		if err := json.Unmarshal([]byte(payload), &identity); err != nil {
			t.Fatal(err)
		}
		meta, err := st.GetIssueSourceMaps(ctx, app.AccountID, app.ID, identity.DeploymentID)
		if err != nil || !meta.Ready {
			t.Fatal("imaging notified before source maps were ready", err)
		}
		return errors.New("lost notify; durable image poll must recover")
	})
	cache := NewCache(t.TempDir())
	b := New(st, notifier, vm, cache, NewDetector(), nil, Config{}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	result, err := b.ProcessOne(ctx, build.ID)
	if err != nil || result.BuildID != build.ID || vm.spawnCalls != 1 || boots != 1 || result.IssueSourceMaps == nil {
		t.Fatalf("publication retry rebuilt or lost artifacts: %+v %v", result, err)
	}
	var attempts int
	if err := pool.QueryRow(ctx, "SELECT last_value FROM source_map_write_attempt").Scan(&attempts); err != nil || attempts != 2 {
		t.Fatal("publication retry was not exercised", err, attempts)
	}
	work, err := st.ListBuildsAwaitingImage(ctx, "", 16)
	if err != nil || len(work) != 1 || work[0].DeploymentID != dep.ID {
		t.Fatal("lost notification recovery missing", err)
	}
	// A new deployment of the same source gets the image and sanitized maps
	// from one verified cache entry, bound to its own deployment ID.
	second, err := st.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Kind: dep.Kind, SourcePath: source, SourceBytes: dep.SourceBytes, LogPath: filepath.Join(t.TempDir(), "build.log")})
	if err != nil {
		t.Fatal(err)
	}
	queued, err := st.CreateBuild(ctx, second.ID, second.Kind, dep.SourceBytes, second.LogPath)
	if err != nil {
		t.Fatal(err)
	}
	result, err = b.ProcessOne(ctx, queued.ID)
	if err != nil || !result.CacheHit || vm.spawnCalls != 1 || boots != 2 {
		t.Fatalf("cache did not carry maps into the new deployment: %+v %v", result, err)
	}
	meta, err := st.GetIssueSourceMaps(ctx, app.AccountID, app.ID, second.ID)
	if err != nil || !meta.Ready || meta.DeploymentID != second.ID || meta.MapCount != 1 {
		t.Fatal("cache reused the old deployment identity", err)
	}
	// Source content remains absent from the persisted canonical request.
	var raw string
	if err := pool.QueryRow(ctx, "SELECT canonical_manifest::text FROM issue_source_map_artifacts WHERE deployment_id=$1", second.ID).Scan(&raw); err != nil || strings.Contains(raw, "sourcesContent") {
		t.Fatal("unsafe persisted metadata", err)
	}
	// A definitive immutable-manifest conflict must become a visible failed
	// deployment, rather than remaining running until the timeout reaper.
	third, err := st.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Kind: dep.Kind, SourcePath: source, SourceBytes: dep.SourceBytes, LogPath: filepath.Join(t.TempDir(), "build.log")})
	if err != nil {
		t.Fatal(err)
	}
	conflicting := testBuildSourceMapRequest()
	conflicting.Manifest.Artifacts[0].SHA256 = strings.Repeat("d", 64)
	if _, err := st.PublishIssueSourceMaps(ctx, app.AccountID, app.ID, third.ID, app.AccountID, conflicting); err != nil {
		t.Fatal(err)
	}
	queued, err = st.CreateBuild(ctx, third.ID, third.Kind, dep.SourceBytes, third.LogPath)
	if err != nil {
		t.Fatal(err)
	}
	_, err = b.ProcessOne(ctx, queued.ID)
	if !errors.Is(err, state.ErrIssueSourceMapsConflict) {
		t.Fatal("expected publication conflict", err)
	}
	failed, err := st.BuildByID(ctx, queued.ID)
	if err != nil || failed.Status != state.BuildFailed || failed.FailureClass != state.FailureUserError {
		t.Fatal("publication conflict left build running", err)
	}
	failedDep, err := st.DeploymentByID(ctx, third.ID)
	if err != nil || failedDep.Status != state.DeployFailed || failedDep.RootfsPath != "" || boots != 2 || vm.spawnCalls != 1 {
		t.Fatal("failed publication reached image handoff", err)
	}
	t.Logf("one VM; two deployments; %d completed handoffs; publication interruption recovered", boots)
}
