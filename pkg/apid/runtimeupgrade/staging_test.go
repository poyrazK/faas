package runtimeupgrade

// adr: 691

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/storage"
	"golang.org/x/sys/unix"
)

// Synthetic archive and qualification exercise handoff/DB fences, not VMs.
const stagingPayload = "synthetic retained archive bytes"

func stagingFixture(t *testing.T, store state.Store) (Stager, state.RuntimeUpgradeOperationRequest, state.Deployment) {
	return stagingCandidateFixture(t, store, true)
}

func stagingCandidateFixture(t *testing.T, store state.Store, createCandidate bool) (Stager, state.RuntimeUpgradeOperationRequest, state.Deployment) {
	t.Helper()
	root := t.TempDir()
	backend, err := storage.NewLocalStorageBackend(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	stager := Stager{Store: store.(StagingStore), Source: backend, SpoolRoot: root}
	account, err := store.CreateAccount(t.Context(), uuid.NewString()+"@staging.test", api.PlanFree)
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(t.Context(), state.App{AccountID: account.ID, Slug: "staged-" + uuid.NewString()[:8], Type: state.AppTypeFunction, Runtime: "node22"})
	if err != nil {
		t.Fatal(err)
	}
	sha := sha256.Sum256([]byte(stagingPayload))
	input := state.Deployment{AppID: app.ID, Kind: state.DeploymentKindTarball, SourceSHA256: hex.EncodeToString(sha[:]), SourceBytes: int64(len(stagingPayload)), SourcePath: filepath.Join(root, "serving.tar.gz"), Handler: "index.handler"}
	if !createCandidate {
		input.OverrideEnv = []byte(`{"REVIEWED_INPUT":"retained-value"}`)
		input.OverrideCmd = []string{"reviewed-command"}
		input.DisableStartupCPUBoost = true
		input.RollbackOn5xx = true
	}
	if err := os.WriteFile(input.SourcePath, []byte(stagingPayload), 0o600); err != nil {
		t.Fatal(err)
	}
	serving, err := store.CreateDeployment(t.Context(), input)
	if err != nil {
		t.Fatal(err)
	}
	build, err := store.CreateBuildWithID(t.Context(), uuid.NewString(), serving.ID, serving.Kind, serving.SourceBytes, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.ClaimQueuedBuild(t.Context(), build.ID); err != nil {
		t.Fatal(err)
	}
	if err := store.UpdateBuildStatus(t.Context(), build.ID, state.BuildSucceeded, "", false, true); err != nil {
		t.Fatal(err)
	}
	releases := store.(state.RuntimeReleaseStore)
	var target state.RuntimeRelease
	for _, n := range []string{"1", "2"} {
		r := state.RuntimeRelease{Runtime: "node22", Architecture: "amd64", SourceRef: "ghcr.io/test/node@sha256:" + strings.Repeat(n, 64), GuestInitSHA256: strings.Repeat("a", 64), LayoutVersion: "test-layout", BaseSHA256: strings.Repeat("b", 64)}
		r.ID = r.Identity()
		target, err = releases.PublishRuntimeRelease(t.Context(), r)
		if err != nil {
			t.Fatal(err)
		}
		if n == "1" {
			key := "apps/" + app.Slug + "/serving.ext4"
			if err := store.SetDeploymentRootfs(t.Context(), serving.ID, "/synthetic-serving", key, 20); err != nil {
				t.Fatal(err)
			}
			if err := releases.BindDeploymentRuntimeRelease(t.Context(), serving.ID, key, target.ID); err != nil {
				t.Fatal(err)
			}
		}
	}
	now := time.Now().UTC().Truncate(time.Microsecond)
	proof := state.RuntimeReleaseQualification{ReleaseID: target.ID, Profile: state.RuntimeQualificationProfile, Architecture: target.Architecture,
		HostID: uuid.NewString(), KernelBootID: uuid.NewString(), SourceCommit: strings.Repeat("a", 40), KernelSHA256: strings.Repeat("b", 64),
		FirecrackerSHA256: strings.Repeat("c", 64), ReportSHA256: strings.Repeat("d", 64), TestMetalSHA256: strings.Repeat("e", 64), LeakcheckSHA256: strings.Repeat("f", 64), StartedAt: now.Add(-2 * time.Minute), CompletedAt: now.Add(-time.Minute)}
	if _, err := store.(state.RuntimeReleaseQualificationStore).RecordRuntimeReleaseQualification(t.Context(), proof); err != nil {
		t.Fatal(err)
	}
	if err := store.MarkDeploymentLive(t.Context(), serving.ID); err != nil {
		t.Fatal(err)
	}
	id := uuid.NewString()
	input.SourcePath, err = stager.CandidatePath(id)
	if err != nil {
		t.Fatal(err)
	}
	input.TrafficPercentExplicit = true
	candidate := state.Deployment{ID: uuid.NewString()}
	if createCandidate {
		candidate, err = store.CreateDeployment(t.Context(), input)
		if err != nil {
			t.Fatal(err)
		}
	}
	serving, err = store.DeploymentByID(t.Context(), serving.ID)
	if err != nil {
		t.Fatal(err)
	}
	return stager, state.RuntimeUpgradeOperationRequest{ID: id, AccountID: account.ID, AppID: app.ID, DeploymentID: candidate.ID, ServingDeploymentID: serving.ID, TargetReleaseID: target.ID, SourceSHA256: input.SourceSHA256, QualificationReportSHA256: proof.ReportSHA256}, serving
}

type stagingResponseLoss struct{ StagingStore }

var errLostRegistrationResponse = errors.New("synthetic lost registration response")

func (s stagingResponseLoss) RegisterRuntimeUpgradeOperation(ctx context.Context, r state.RuntimeUpgradeOperationRequest) (state.RuntimeUpgradeOperation, error) {
	if _, err := s.StagingStore.RegisterRuntimeUpgradeOperation(ctx, r); err != nil {
		return state.RuntimeUpgradeOperation{}, err
	}
	return state.RuntimeUpgradeOperation{}, errLostRegistrationResponse
}

func TestStagingRegistrationResponseLossAndQueueRecovery(t *testing.T) {
	for _, kind := range []string{"memory", "postgres"} {
		t.Run(kind, func(t *testing.T) {
			var store state.Store = state.NewMemStore()
			if kind == "postgres" {
				pool := pgtest.OpenMigrated(t)
				if err := db.MigrateUp(t.Context(), pool); err != nil {
					t.Fatal(err)
				}
				store = state.NewPgStore(pool)
			}
			s, r, serving := stagingFixture(t, store)
			// Prefer the immutable retained build object even if the old spool
			// file is corrupt. New source is never fetched from mutable Git.
			if err := s.Source.Put(t.Context(), "sources/"+serving.BuildID+".tar.gz", strings.NewReader(stagingPayload)); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(serving.SourcePath, []byte("corrupt"), 0o600); err != nil {
				t.Fatal(err)
			}
			s.Store = stagingResponseLoss{s.Store}
			if _, err := s.StageAndRegister(t.Context(), r); !errors.Is(err, errLostRegistrationResponse) {
				t.Fatal("response loss not injected")
			}
			ops := store.(StagingStore)
			first, err := ops.RuntimeUpgradeOperation(t.Context(), r.AccountID, r.ID)
			if err != nil || first.Phase != state.RuntimeUpgradePrepared {
				t.Fatal(first, err)
			}
			if _, err := store.BuildByDeployment(t.Context(), r.DeploymentID); !errors.Is(err, state.ErrNotFound) {
				t.Fatal("staging queued a build", err)
			}
			path, err := s.CandidatePath(r.ID)
			if err != nil {
				t.Fatal(err)
			}
			bytes, err := os.ReadFile(path)
			if err != nil || string(bytes) != stagingPayload {
				t.Fatal("candidate spool differs", err)
			}
			// Registration retry must perform no storage I/O once committed.
			s.Source = failingSource{StorageBackend: s.Source}
			replay, err := s.StageAndRegister(t.Context(), r)
			if err != nil || replay != first {
				t.Fatal("lost response replaced operation", replay, err)
			}
			if worked, err := (Executor{Store: ops}).RunOnce(t.Context()); !worked || err != nil {
				t.Fatal("staged operation not recoverable", worked, err)
			}
			build, err := store.BuildByDeployment(t.Context(), r.DeploymentID)
			if err != nil || build.ID != r.ID || build.Status != state.BuildQueued {
				t.Fatal(build, err)
			}
			current, err := store.DeploymentByID(t.Context(), serving.ID)
			if err != nil || current.Status != state.DeployLive || current.TrafficPercent != 100 {
				t.Fatal("staging changed serving traffic", current, err)
			}
		})
	}
}

type failingSource struct{ storage.StorageBackend }

func (failingSource) Get(context.Context, string) (io.ReadCloser, error) {
	return nil, errors.New("synthetic storage outage")
}

type interruptedSource struct {
	storage.StorageBackend
	puts int
}

func (s *interruptedSource) Put(ctx context.Context, key string, r io.Reader) error {
	s.puts++
	if err := s.StorageBackend.Put(ctx, key, r); err != nil {
		return err
	}
	return errors.New("synthetic lost upload response")
}

func TestStagingInterruptedPublicationReusesVerifiedObject(t *testing.T) {
	s, r, _ := stagingFixture(t, state.NewMemStore())
	backend := &interruptedSource{StorageBackend: s.Source}
	s.Source = backend
	if _, err := s.StageAndRegister(t.Context(), r); err == nil || backend.puts != 1 {
		t.Fatal("upload response loss not injected", backend.puts, err)
	}
	if _, err := s.Store.RuntimeUpgradeOperation(t.Context(), r.AccountID, r.ID); !errors.Is(err, state.ErrNotFound) {
		t.Fatal("registered incomplete publication", err)
	}
	if _, err := s.StageAndRegister(t.Context(), r); err != nil || backend.puts != 1 {
		t.Fatal("retry rewrote published object", backend.puts, err)
	}
}

type publicationFault struct {
	storage.StorageBackend
	after   func() error
	corrupt bool
}

func (s publicationFault) Put(ctx context.Context, key string, r io.Reader) error {
	if s.corrupt {
		r = strings.NewReader("corrupt upload")
	}
	if err := s.StorageBackend.Put(ctx, key, r); err != nil {
		return err
	}
	if s.after != nil {
		return s.after()
	}
	return nil
}

func TestStagingRechecksPublishedBytesAndQualification(t *testing.T) {
	for _, fault := range []string{"corrupt_successful_upload", "revoked_during_publication", "canceled"} {
		t.Run(fault, func(t *testing.T) {
			store := state.NewMemStore()
			s, r, serving := stagingFixture(t, store)
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			backend := publicationFault{StorageBackend: s.Source, corrupt: fault == "corrupt_successful_upload"}
			if fault == "revoked_during_publication" {
				backend.after = func() error {
					return store.RevokeRuntimeReleaseQualification(ctx, r.TargetReleaseID, r.QualificationReportSHA256, strings.Repeat("a", 64))
				}
			}
			if fault == "canceled" {
				cancel()
			}
			s.Source = backend
			want := ErrSourceIntegrity
			if fault == "revoked_during_publication" {
				want = state.ErrConflict
			}
			if fault == "canceled" {
				want = context.Canceled
			}
			if _, err := s.StageAndRegister(ctx, r); !errors.Is(err, want) {
				t.Fatal("changed handoff/qualification accepted", err)
			}
			if _, err := store.RuntimeUpgradeOperation(t.Context(), r.AccountID, r.ID); !errors.Is(err, state.ErrNotFound) {
				t.Fatal("failed preparation committed", err)
			}
			if _, err := store.DeploymentRuntimeUpgradeTarget(t.Context(), r.DeploymentID); !errors.Is(err, state.ErrNotFound) {
				t.Fatal("failed registration retained pin", err)
			}
			if _, err := store.BuildByDeployment(t.Context(), r.DeploymentID); !errors.Is(err, state.ErrNotFound) {
				t.Fatal("failed preparation queued build", err)
			}
			current, err := store.DeploymentByID(t.Context(), serving.ID)
			if err != nil || current.TrafficPercent != 100 || current.Status != state.DeployLive {
				t.Fatal(current, err)
			}
		})
	}
}

func TestStagingRejectsCorruptSourceAndUnsafePaths(t *testing.T) {
	for _, fault := range []string{"digest", "short", "long", "symlink", "directory_link", "fifo", "outside", "candidate_corrupt", "candidate_link", "object_corrupt", "outage", "foreign_account", "invalid_id", "noncanonical_operation", "changed_handler"} {
		t.Run(fault, func(t *testing.T) {
			s, r, serving := stagingFixture(t, state.NewMemStore())
			path, err := s.CandidatePath(r.ID)
			if err != nil {
				t.Fatal(err)
			}
			switch fault {
			case "digest", "short", "long":
				payload := strings.Repeat("x", len(stagingPayload))
				if fault == "short" {
					payload = stagingPayload[:2]
				}
				if fault == "long" {
					payload = stagingPayload + "extra"
				}
				if err := s.Source.Put(t.Context(), "sources/"+serving.BuildID+".tar.gz", strings.NewReader(payload)); err != nil {
					t.Fatal(err)
				}
			case "symlink", "fifo":
				if err := os.Remove(serving.SourcePath); err != nil {
					t.Fatal(err)
				}
				if fault == "symlink" {
					outside := filepath.Join(t.TempDir(), "archive")
					if err := os.WriteFile(outside, []byte(stagingPayload), 0o600); err != nil {
						t.Fatal(err)
					}
					if err := os.Symlink(outside, serving.SourcePath); err != nil {
						t.Fatal(err)
					}
				} else if err := unix.Mkfifo(serving.SourcePath, 0o600); err != nil {
					t.Fatal(err)
				}
			case "directory_link":
				// A directory symlink is rejected even when its target is inside.
				if err := os.Symlink(s.SpoolRoot, filepath.Join(s.SpoolRoot, "linked")); err != nil {
					t.Fatal(err)
				}
				serving.SourcePath = filepath.Join(s.SpoolRoot, "linked", "serving.tar.gz")
				if err := s.stageSource(t.Context(), r.ID, serving); !errors.Is(err, ErrSourceIntegrity) {
					t.Fatal(err)
				}
				return
			case "outside":
				serving.SourcePath = filepath.Join(t.TempDir(), "archive")
				if err := s.stageSource(t.Context(), r.ID, serving); !errors.Is(err, ErrSourceIntegrity) {
					t.Fatal(err)
				}
				return
			case "candidate_corrupt":
				if err := os.WriteFile(path, []byte(strings.Repeat("x", len(stagingPayload))), 0o600); err != nil {
					t.Fatal(err)
				}
			case "candidate_link":
				if err := os.Symlink(serving.SourcePath, path); err != nil {
					t.Fatal(err)
				}
			case "object_corrupt":
				if err := s.Source.Put(t.Context(), "sources/"+r.ID+".tar.gz", strings.NewReader("corrupt")); err != nil {
					t.Fatal(err)
				}
			case "outage":
				s.Source = failingSource{s.Source}
			case "foreign_account":
				r.AccountID = uuid.NewString()
			case "invalid_id":
				r.ID = "../escape"
			case "noncanonical_operation":
				r.ID = strings.ReplaceAll(r.ID, "-", "")
			case "changed_handler":
				// A read seam simulates concurrent candidate input drift.
				s.Store = changedStagingCandidate{s.Store, r.DeploymentID}
			}
			want := ErrSourceIntegrity
			switch fault {
			case "foreign_account":
				want = state.ErrNotFound
			case "invalid_id", "noncanonical_operation":
				want = state.ErrInvalidArgument
			case "changed_handler":
				want = state.ErrConflict
			}
			if _, err := s.StageAndRegister(t.Context(), r); err == nil || (fault != "outage" && !errors.Is(err, want)) {
				t.Fatal("unsafe source admitted or wrong gate", err)
			}
			if _, err := s.Store.RuntimeUpgradeOperation(t.Context(), r.AccountID, r.ID); err == nil {
				t.Fatal("unsafe staging registered operation")
			}
			if _, err := s.Store.DeploymentByID(t.Context(), serving.ID); err != nil {
				t.Fatal(err)
			}
		})
	}
}

type changedStagingCandidate struct {
	StagingStore
	id string
}

func (s changedStagingCandidate) DeploymentByID(ctx context.Context, id string) (state.Deployment, error) {
	d, err := s.StagingStore.DeploymentByID(ctx, id)
	if id == s.id {
		d.Handler = "different.handler"
	}
	return d, err
}

func TestStagingConcurrentPublicationNeverOverwrites(t *testing.T) {
	s, r, serving := stagingFixture(t, state.NewMemStore())
	var wg sync.WaitGroup
	errs := make(chan error, 4)
	for range 4 {
		wg.Add(1)
		go func() { defer wg.Done(); errs <- s.stageSource(t.Context(), r.ID, serving) }()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	files, err := os.ReadDir(s.SpoolRoot)
	if err != nil || len(files) != 2 {
		t.Fatal("partial staging files retained", files, err)
	}
}
