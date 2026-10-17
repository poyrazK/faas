package cirunner

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"io/fs"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/ci"
)

type StepExecutor interface {
	Run(context.Context, StepExecution, io.Writer) (int, error)
}

// StepExecution contains only a prepared step's explicit environment and pinned
// checkout directory. The directory is valid until Run returns; executors must
// not retain it or inherit their own process environment.
type StepExecution struct {
	Step        ci.Step
	Directory   *os.File
	Environment []string
}

// Runner requires an explicit executor. Only the guest entrypoint constructs the
// OS executor; portable tests substitute one that writes synthetic files.
type Runner struct {
	Manifest   ci.JobsManifest
	Transport  *Transport
	Executor   StepExecutor
	WorkParent string
}

func (r Runner) Run(ctx context.Context) (ci.JobsResultManifest, error) {
	if r.Transport == nil || r.Executor == nil || ci.ValidateJobsManifest(r.Manifest) != nil || r.Manifest.AttemptID != r.Transport.proof.RunID {
		return ci.JobsResultManifest{}, errTransfer
	}
	work, err := os.MkdirTemp(r.WorkParent, "gregale-ci-work-*")
	if err != nil {
		return ci.JobsResultManifest{}, errSource
	}
	defer func() { _ = os.RemoveAll(work) }()
	root, err := os.OpenRoot(work)
	if err != nil {
		return ci.JobsResultManifest{}, errSource
	}
	defer func() { _ = root.Close() }()
	logs := &prefixLog{}
	code := 125
	result := ci.JobsResultManifest{Version: 1, AccountID: r.Manifest.AccountID, ExecutionID: r.Manifest.ExecutionID, AttemptID: r.Manifest.AttemptID, CommitSHA: r.Manifest.CommitSHA, Status: ci.StatusFailed, ExitCode: &code}
	for _, step := range r.Manifest.Job.Steps {
		result.Steps = append(result.Steps, ci.StepResult{Name: step.Name, Status: ci.StatusSkipped})
	}
	workCtx, cancel := context.WithDeadline(ctx, r.Manifest.Deadline)
	defer cancel()
	view, err := r.Transport.Control(workCtx)
	if err == nil && (!sameManifest(view.Manifest, r.Manifest) || view.CancelRequested || view.Sealed) {
		err = errTransfer
	}
	if err == nil {
		err = r.checkout(workCtx, root)
	}
	if err == nil {
		err = r.prepareInputs(workCtx, root)
	}
	if err == nil && len(r.Manifest.Inputs) > 0 {
		// Observe cancellation or a replaced runtime lease received while input
		// bytes were arriving, before the first repository command can start.
		latest, controlErr := r.Transport.Control(workCtx)
		if controlErr != nil || !sameManifest(latest.Manifest, r.Manifest) || latest.CancelRequested || latest.Sealed {
			err = errTransfer
		}
	}
	if err == nil {
		var progress *liveProgress
		if view.ProgressEnabled {
			progress = startLiveProgress(workCtx, r.Transport, r.Manifest.Job, logs)
		}
		result = r.execute(workCtx, cancel, root, logs, result, progress)
		// Stop the sender before final output; a lost final progress response can
		// be retried, while the independent seal still decides output completeness.
		if progress != nil {
			finalCtx, finish := context.WithTimeout(context.WithoutCancel(ctx), time.Duration(api.CIJobsProgressSeconds)*time.Second)
			progress.finish(finalCtx, result.Steps)
			finish()
		}
	} else {
		_, _ = logs.Write([]byte("CI source/control/input preparation failed\n"))
	}
	// The existing guest supervisor permits 30 seconds after SIGTERM. Final
	// output gets a smaller independent deadline so cancelled steps can publish.
	finalCtx, finish := context.WithTimeout(context.WithoutCancel(ctx), time.Duration(api.CIJobsFinalizeSeconds)*time.Second)
	defer finish()
	if err := r.publish(finalCtx, root, logs, &result); err != nil {
		return result, errTransfer
	}
	return result, nil
}

func sameManifest(a, b ci.JobsManifest) bool {
	x, _ := json.Marshal(a)
	y, _ := json.Marshal(b)
	return bytes.Equal(x, y)
}

func (r Runner) checkout(ctx context.Context, root *os.Root) error {
	body, err := r.Transport.source(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = body.Close() }()
	archive, err := os.CreateTemp(r.WorkParent, "gregale-ci-archive-*")
	if err != nil {
		return errSource
	}
	defer func() { _ = archive.Close(); _ = os.Remove(archive.Name()) }()
	hash := sha256.New()
	count, err := io.Copy(io.MultiWriter(archive, hash), io.LimitReader(body, r.Manifest.Source.Bytes+1))
	if err != nil || count != r.Manifest.Source.Bytes || "sha256:"+hex.EncodeToString(hash.Sum(nil)) != r.Manifest.Source.Digest {
		return errSource
	}
	if _, err := archive.Seek(0, io.SeekStart); err != nil {
		return errSource
	}
	return extract(ctx, archive, root)
}

func (r Runner) execute(ctx context.Context, cancel context.CancelFunc, root *os.Root, logs *prefixLog, result ci.JobsResultManifest, progress *liveProgress) ci.JobsResultManifest {
	pollCtx, stop := context.WithCancel(ctx)
	done := make(chan struct{})
	var controlFailed atomic.Bool
	go func() {
		defer close(done)
		ticker := time.NewTicker(time.Duration(api.CIJobsControlPollMillis) * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-pollCtx.Done():
				return
			case <-ticker.C:
				view, err := r.Transport.Control(pollCtx)
				if pollCtx.Err() != nil {
					return
				}
				if err != nil || !sameManifest(view.Manifest, r.Manifest) || view.CancelRequested {
					controlFailed.Store(err != nil)
					cancel()
					return
				}
			}
		}
	}()
	defer func() { stop(); <-done }()
	zero := 0
	result.Status = ci.StatusSucceeded
	result.ExitCode = &zero
	for index, step := range r.Manifest.Job.Steps {
		if ctx.Err() != nil {
			code := 143
			result.Status = ci.StatusCancelled
			result.ExitCode = &code
			break
		}
		var started *time.Time
		if progress != nil {
			at := time.Now().UTC()
			started = &at
			progress.set(index, ci.StepResult{Name: step.Name, Status: ci.StatusRunning, StartedAt: started})
		}
		code, err := r.runStep(ctx, root, step, logs)
		state := ci.StatusSucceeded
		if ctx.Err() != nil {
			state = ci.StatusCancelled
			code = 143
		} else if err != nil {
			state = ci.StatusFailed
			code = 125
		} else if code != 0 {
			state = ci.StatusFailed
		}
		result.Steps[index] = ci.StepResult{Name: step.Name, Status: state, StartedAt: started}
		if progress != nil {
			at := time.Now().UTC()
			result.Steps[index].FinishedAt = &at
		}
		if state != ci.StatusCancelled {
			result.Steps[index].ExitCode = &code
		}
		progress.set(index, result.Steps[index])
		if state != ci.StatusSucceeded {
			result.Status = state
			result.ExitCode = &code
			break
		}
	}
	if controlFailed.Load() {
		code := 125
		result.Status = ci.StatusFailed
		result.ExitCode = &code
	}
	return result
}

func (r Runner) publish(ctx context.Context, root *os.Root, logs *prefixLog, result *ci.JobsResultManifest) error {
	raw, truncated := logs.snapshot()
	object := ci.RuntimeObject{Name: "logs.bin", Digest: blobDigest(raw), Bytes: int64(len(raw))}
	if err := r.uploadBytes(ctx, object, bytes.NewReader(raw)); err != nil {
		return err
	}
	result.Logs = ci.SourceArchive{Key: r.Manifest.OutputPrefix + object.Name, Digest: object.Digest, Bytes: object.Bytes}
	result.LogTruncated = truncated
	total, count := int64(0), 0
	visited := 0
	err := fs.WalkDir(root.FS(), ".", func(name string, entry fs.DirEntry, walkErr error) error {
		visited++
		if visited > api.CIJobsSourceMaxEntries {
			return errTransfer
		}
		if walkErr != nil {
			return errTransfer
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if entry.IsDir() {
			if name != "." && !ci.JobsArtifactDirectoryNeeded(r.Manifest.Job.Artifacts, name) {
				return fs.SkipDir
			}
			return nil
		}
		if !ci.JobsArtifactSelected(r.Manifest.Job.Artifacts, name) {
			return nil
		}
		if count >= api.CIMaxArtifactsPerJob || entry.Type()&os.ModeSymlink != 0 {
			return errTransfer
		}
		file, err := openArtifact(root, name)
		if err != nil {
			return errTransfer
		}
		defer func() { _ = file.Close() }()
		info, err := file.Stat()
		if err != nil || !info.Mode().IsRegular() || info.Size() > api.CIArtifactMaxBytes || total+info.Size() > api.CIArtifactsMaxBytesPerJob {
			return errTransfer
		}
		artifact, err := r.spoolArtifact(ctx, name, file, info.Size())
		if err != nil {
			return err
		}
		result.Artifacts = append(result.Artifacts, artifact)
		count++
		total += artifact.Bytes
		return nil
	})
	if err != nil {
		return err
	}
	return r.Transport.seal(ctx, *result)
}

func (r Runner) spoolArtifact(ctx context.Context, name string, reader io.Reader, size int64) (ci.JobsArtifact, error) {
	file, err := os.CreateTemp(r.WorkParent, "gregale-ci-artifact-*")
	if err != nil {
		return ci.JobsArtifact{}, errTransfer
	}
	defer func() { _ = file.Close(); _ = os.Remove(file.Name()) }()
	hash := sha256.New()
	count, err := io.Copy(io.MultiWriter(file, hash), io.LimitReader(reader, size+1))
	if err != nil || count != size {
		return ci.JobsArtifact{}, errTransfer
	}
	object := ci.RuntimeObject{Name: "artifacts/" + name, Digest: "sha256:" + hex.EncodeToString(hash.Sum(nil)), Bytes: count}
	if err := r.Transport.upload(ctx, object, file); err != nil {
		return ci.JobsArtifact{}, err
	}
	return ci.JobsArtifact{ArtifactRef: ci.ArtifactRef{Path: name, Digest: object.Digest, Bytes: count}, Key: r.Manifest.OutputPrefix + object.Name}, nil
}

func (r Runner) uploadBytes(ctx context.Context, object ci.RuntimeObject, reader io.Reader) error {
	file, err := os.CreateTemp(r.WorkParent, "gregale-ci-log-*")
	if err != nil {
		return errTransfer
	}
	defer func() { _ = file.Close(); _ = os.Remove(file.Name()) }()
	if _, err := io.Copy(file, reader); err != nil {
		return errTransfer
	}
	return r.Transport.upload(ctx, object, file)
}

func blobDigest(raw []byte) string {
	sum := sha256.Sum256(raw)
	return "sha256:" + hex.EncodeToString(sum[:])
}

type prefixLog struct {
	mu        sync.Mutex
	raw       []byte
	truncated bool
}

func (l *prefixLog) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	keep := min(len(p), api.CILogMaxBytesPerAttempt-len(l.raw))
	l.raw = append(l.raw, p[:keep]...)
	l.truncated = l.truncated || keep < len(p)
	return len(p), nil
}
func (l *prefixLog) snapshot() ([]byte, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return bytes.Clone(l.raw), l.truncated
}
