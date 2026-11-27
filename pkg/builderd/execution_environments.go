package builderd

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/runenv"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/storage"
	"io"
	"os"
	"path/filepath"
	"time"
)

func (b *Builderd) WithExecutionEnvironments(enabled bool, backend storage.StorageBackend) *Builderd {
	b.executionEnvironmentsEnabled = enabled
	b.executionEnvironmentStorage = backend
	return b
}

// processNextEnvironment shares the ordinary build slot and drain accounting.
// It is reached by the durable poller when the application build queue is empty.
func (b *Builderd) processNextEnvironment(ctx context.Context) (BuildResult, error) {
	store, ok := b.store.(state.ExecutionEnvironmentStore)
	if !b.executionEnvironmentsEnabled || !ok || b.executionEnvironmentStorage == nil {
		return BuildResult{}, state.ErrNotFound
	}
	_, release, acquired := b.acquireSlot()
	if !acquired {
		return BuildResult{}, ErrNoSlot
	}
	defer release()
	row, err := store.ClaimExecutionEnvironment(ctx, "build")
	if err != nil {
		return BuildResult{}, err
	}
	_ = b.environmentDiagnostic(ctx, row, "build", "progress", "Starting the isolated dependency builder.")
	buildCtx, cancel := context.WithTimeout(ctx, time.Duration(api.BuildTimeoutSeconds)*time.Second)
	defer cancel()
	failureDiagnosticText := ""
	fail := func(code string, causes ...error) (BuildResult, error) {
		cleanup, c := context.WithTimeout(context.WithoutCancel(ctx), api.ExecutionEnvironmentCleanupSeconds*time.Second)
		defer c()
		publicCode, message := runenv.DiagnosticFailure(code)
		if failureDiagnosticText != "" {
			publicCode, message = runenv.DiagnosticFailure(failureDiagnosticText)
		} else if len(causes) > 0 && causes[0] != nil {
			publicCode, message = runenv.DiagnosticFailure(causes[0].Error())
		}
		_ = b.environmentDiagnostic(cleanup, row, "build", publicCode, message)
		_ = store.FailExecutionEnvironment(cleanup, row, code)
		if len(causes) > 0 && causes[0] != nil {
			return BuildResult{}, fmt.Errorf("builderd: Run environment %s: %w", code, causes[0])
		}
		return BuildResult{}, fmt.Errorf("builderd: Run environment %s", code)
	}
	acct, err := b.store.AccountByID(buildCtx, row.AccountID)
	if err != nil || !acct.Active() {
		return fail("account_inactive")
	}
	recipeDigest, digestErr := runenv.Digest(row.Recipe, row.BaseRef)
	if digestErr != nil || recipeDigest != row.RecipeDigest {
		return fail("build_failed")
	}
	source, err := runenv.SourceTar(row.ID, row.Recipe, row.BaseRef)
	if err != nil {
		return fail("build_failed")
	}
	sourceDir := b.cfg.SourceSpoolDir
	if sourceDir != "" {
		if err := os.MkdirAll(sourceDir, 0700); err != nil {
			return fail("build_failed")
		}
	}
	f, err := os.CreateTemp(sourceDir, "run-environment-*.tar.gz")
	if err != nil {
		return fail("build_failed")
	}
	sourcePath := f.Name()
	defer func() { _ = os.Remove(sourcePath) }()
	_, writeErr := f.Write(source)
	closeErr := f.Close()
	if writeErr != nil || closeErr != nil {
		return fail("build_failed")
	}
	hash := sha256.Sum256(source)
	handle, err := b.vm.Spawn(buildCtx, VMRequest{BuildID: row.Lease, TenantID: row.AccountID, DeploymentID: row.ID, SourcePath: sourcePath, SourceSHA256: hex.EncodeToString(hash[:]), DockerfilePath: "Dockerfile", Framework: FrameworkDocker, Runtime: string(row.Runtime), RAMMB: api.BuildVMRAMMB, TimeoutSec: api.BuildTimeoutSeconds, Plan: string(acct.Plan)})
	if err != nil {
		return fail("build_failed", err)
	}
	if !b.registerActiveVM(row.Lease, handle) {
		_ = b.vm.Cancel(buildCtx, row.Lease)
		_, _ = b.vm.WaitForCompletion(buildCtx, handle)
		return fail("build_failed")
	}
	defer b.unregisterActiveVM(row.Lease)
	watchDone := make(chan struct{})
	watchStop := make(chan struct{})
	go func() {
		defer close(watchDone)
		ticker := time.NewTicker(api.ExecutionEnvironmentPollSeconds * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-watchStop:
				return
			case <-buildCtx.Done():
				return
			case <-ticker.C:
				live, err := store.ExecutionEnvironmentByID(buildCtx, row.AccountID, row.ID)
				account, acctErr := b.store.AccountByID(buildCtx, row.AccountID)
				if err != nil || acctErr != nil || !account.Active() || live.Status != row.Status || live.Lease != row.Lease {
					cancel()
					return
				}
			}
		}
	}()
	stopLogs := b.environmentLogs(buildCtx, row, handle)
	outcome, waitErr := b.vm.WaitForCompletion(buildCtx, handle)
	stopLogs()
	b.environmentLogTail(context.WithoutCancel(buildCtx), row, outcome.LogTail)
	close(watchStop)
	<-watchDone
	defer cleanupEnvironmentExport(outcome, row.Lease)
	if waitErr != nil || outcome.ExitCode != 0 || buildCtx.Err() != nil {
		if waitErr != nil {
			return fail("build_failed", waitErr)
		}
		failureDiagnosticText = outcome.LogTail
		return fail("build_failed", fmt.Errorf("dependency builder exit %d", outcome.ExitCode))
	}
	if !filepath.IsAbs(outcome.ExportDir) || filepath.Base(outcome.ExportDir) != row.Lease || outcome.OCIImage != filepath.Join(outcome.ExportDir, "build", "out", "image.tar") {
		return fail("build_failed")
	}
	archiveRoot, err := os.OpenRoot(outcome.ExportDir)
	if err != nil {
		return fail("build_failed", err)
	}
	defer func() { _ = archiveRoot.Close() }()
	archive, err := archiveRoot.Open("build/out/image.tar")
	if err != nil {
		return fail("build_failed")
	}
	defer func() { _ = archive.Close() }()
	info, err := archive.Stat()
	cap := api.ExecutionEnvironmentBytesByPlan[acct.Plan.PlanIndex()]
	if err != nil || !info.Mode().IsRegular() || info.Size() <= 0 || info.Size() > cap {
		return fail("build_failed")
	}
	sum := sha256.New()
	n, err := io.Copy(sum, io.LimitReader(archive, cap+1))
	if err != nil || n != info.Size() {
		return fail("build_failed")
	}
	if _, err = archive.Seek(0, io.SeekStart); err != nil {
		return fail("build_failed")
	}
	if err = b.executionEnvironmentStorage.Put(buildCtx, row.ExportKey, io.LimitReader(archive, n)); err != nil {
		return fail("build_failed")
	}
	_ = b.environmentDiagnostic(buildCtx, row, "export", "progress", "Verified OCI export; queued for image verification and vulnerability scanning.")
	if err = store.PublishExecutionEnvironmentBuild(buildCtx, row, hex.EncodeToString(sum.Sum(nil))); err != nil {
		return BuildResult{}, err
	}
	return BuildResult{BuildID: row.ID}, nil
}

func cleanupEnvironmentExport(outcome BuildOutcome, lease string) {
	if filepath.Base(outcome.ExportDir) == lease && outcome.OCIImage == filepath.Join(outcome.ExportDir, "build", "out", "image.tar") {
		_ = os.RemoveAll(outcome.ExportDir)
	}
}
