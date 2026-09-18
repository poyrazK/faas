package imaged

import (
	"context"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/onebox-faas/faas/pkg/oci"
	"github.com/onebox-faas/faas/pkg/rootfs"
	"github.com/onebox-faas/faas/pkg/sched"
	"github.com/onebox-faas/faas/pkg/state"
)

const (
	jobMaterializationBatchSize   = 32
	jobMaterializationLease       = 15 * time.Minute
	jobMaterializationMaxAttempts = 3
	jobMaterializationRetryBase   = 5 * time.Second
	jobMaterializationRetryMax    = 5 * time.Minute
)

// MaterializeJob resolves a job's source OCI reference, builds a complete
// ext4 rootfs, and publishes it under sched.JobLayerKey. The source reference
// is re-read at the final state update; if a customer changed image_ref while
// the build was running, the stale artifact is not marked ready.
func (h *Handler) MaterializeJob(ctx context.Context, jobID string) error {
	images, ok := h.store.(state.JobImageMaterializationStore)
	if !ok {
		return fmt.Errorf("imaged: job image materialization store unavailable")
	}
	job, err := h.store.JobGetByID(ctx, jobID)
	if err != nil {
		return fmt.Errorf("imaged: load job %s: %w", jobID, err)
	}
	if job.ImageMaterializationStatus == "ready" && job.ImageStorageKey != "" {
		return nil
	}
	if job.ImageMaterializationStatus == "failed" {
		return fmt.Errorf("imaged: job %s image materialization is terminally failed", jobID)
	}
	if claimer, ok := h.store.(state.JobImageMaterializationClaimer); ok {
		claimed, claimErr := claimer.JobClaimImageMaterialization(ctx, jobID, h.jobMaterializationOwner(), jobMaterializationLease)
		if claimErr != nil {
			if errors.Is(claimErr, state.ErrNotFound) {
				return nil // another worker owns the live lease, or backoff is active
			}
			return fmt.Errorf("imaged: claim job %s materialization: %w", jobID, claimErr)
		}
		job = claimed
	}
	return h.materializeClaimedJob(ctx, images, job)
}

func (h *Handler) materializeClaimedJob(ctx context.Context, images state.JobImageMaterializationStore, job state.Job) (err error) {
	started := time.Now()
	outcome := "error"
	defer func() {
		if h.ops != nil {
			if err == nil {
				outcome = "ready"
			} else if job.ImageMaterializationAttempts >= jobMaterializationMaxAttempts {
				outcome = "failed"
			} else {
				outcome = "retry"
			}
			h.ops.ObserveCode("job_materialization", outcome, time.Since(started))
		}
	}()
	if job.ImageRef == "" {
		return h.failJobMaterialization(ctx, images, job, "image_ref is empty")
	}
	mp, ok := h.oci.(oci.ManifestPuller)
	if !ok {
		return h.failJobMaterialization(ctx, images, job,
			fmt.Sprintf("OCI puller %T does not implement ManifestPuller", h.oci))
	}

	digest, err := pullDigestWithAuth(ctx, h.oci, job.ImageRef, nil)
	if err != nil {
		return h.failJobMaterialization(ctx, images, job, fmt.Sprintf("resolve image: %v", err))
	}
	ref, err := oci.ParseReference(job.ImageRef)
	if err != nil {
		return h.failJobMaterialization(ctx, images, job, fmt.Sprintf("parse image_ref: %v", err))
	}
	resolvedRef := (oci.Reference{Registry: ref.Registry, Repository: ref.Repository, Digest: digest}).String()
	manifest, err := pullManifestWithAuth(ctx, mp, resolvedRef, nil)
	if err != nil {
		return h.failJobMaterialization(ctx, images, job, fmt.Sprintf("pull manifest: %v", err))
	}
	config, err := pullImageConfigWithAuth(ctx, h.oci, resolvedRef, nil)
	if err != nil {
		return h.failJobMaterialization(ctx, images, job, fmt.Sprintf("pull image config: %v", err))
	}
	// A job's command is staged later in job.json, but BuildFullRootfs also
	// injects the stable app.json contract. Use the job command only when the
	// source image has no command of its own so a valid manifest can be built.
	if len(config.Entrypoint) == 0 && len(config.Cmd) == 0 {
		config.Cmd = append([]string(nil), job.Command...)
	}
	appManifest, err := manifestFromImageConfig(config)
	if err != nil {
		return h.failJobMaterialization(ctx, images, job, fmt.Sprintf("image config: %v", err))
	}

	repo := repoWithHost(resolvedRef)
	if repo == "" {
		return h.failJobMaterialization(ctx, images, job, "cannot derive image repository")
	}
	readers := make([]io.Reader, 0, len(manifest.Layers))
	closers := make([]io.Closer, 0, len(manifest.Layers))
	closeAll := func() {
		for _, c := range closers {
			_ = c.Close()
		}
	}
	defer closeAll()
	for _, layer := range manifest.Layers {
		rc, pullErr := pullBlobWithAuth(ctx, mp, repo, layer.Digest, nil)
		if pullErr != nil {
			return h.failJobMaterialization(ctx, images, job, fmt.Sprintf("pull layer %s: %v", layer.Digest, pullErr))
		}
		closers = append(closers, rc)
		readers = append(readers, rc)
	}
	acct, err := h.store.AccountByID(ctx, job.AccountID)
	if err != nil {
		return h.failJobMaterialization(ctx, images, job, fmt.Sprintf("load account: %v", err))
	}
	be, err := h.storageFor()
	if err != nil {
		return h.failJobMaterialization(ctx, images, job, fmt.Sprintf("storage backend: %v", err))
	}
	key := sched.JobLayerKey(job.ID)
	if _, err := h.builder.BuildFullRootfs(ctx, rootfs.BuildFullRootfsInput{
		Layers:        readers,
		Manifest:      appManifest,
		GuestInitPath: h.guestInitPath,
		Plan:          acct.Plan,
		Storage:       be,
		StorageKey:    key,
	}); err != nil {
		return h.failJobMaterialization(ctx, images, job, fmt.Sprintf("build ext4: %v", err))
	}
	if _, err := images.JobSetImageMaterialization(ctx, job.ID, job.ImageRef, "ready", digest, key, ""); err != nil {
		return fmt.Errorf("imaged: publish job %s materialization state: %w", job.ID, err)
	}
	h.log.Info("imaged: materialized job image", "job", job.ID, "digest", digest, "key", key)
	return nil
}

// MaterializePendingJobs drains the bounded pending queue. Transient failures
// remain pending with a durable backoff; after the bounded attempt budget the
// row becomes failed for customer/operator visibility.
func (h *Handler) MaterializePendingJobs(ctx context.Context) error {
	images, ok := h.store.(state.JobImageMaterializationStore)
	if !ok {
		return fmt.Errorf("imaged: job image materialization store unavailable")
	}
	var jobs []state.Job
	if claimer, ok := h.store.(state.JobImageMaterializationClaimer); ok {
		var err error
		jobs, err = claimer.JobClaimPendingImageMaterialization(ctx, jobMaterializationBatchSize, h.jobMaterializationOwner(), jobMaterializationLease)
		if err != nil {
			return err
		}
	} else {
		var err error
		jobs, err = images.JobListPendingImageMaterialization(ctx, jobMaterializationBatchSize)
		if err != nil {
			return err
		}
	}
	for _, job := range jobs {
		if err := h.materializeClaimedJob(ctx, images, job); err != nil {
			h.log.Warn("imaged: pending job image materialization failed", "job", job.ID, "err", err)
		}
	}
	return nil
}

func (h *Handler) failJobMaterialization(ctx context.Context, images state.JobImageMaterializationStore, job state.Job, reason string) error {
	if claimer, ok := h.store.(state.JobImageMaterializationClaimer); ok {
		delay := jobMaterializationRetryDelay(job.ImageMaterializationAttempts)
		updated, err := claimer.JobRecordImageMaterializationFailure(ctx, job.ID, job.ImageRef, h.jobMaterializationOwner(), reason, time.Now().Add(delay), jobMaterializationMaxAttempts)
		if err != nil {
			return fmt.Errorf("imaged: job %s materialization failed (%s), recording retry: %w", job.ID, reason, err)
		}
		if updated.ImageMaterializationStatus == "failed" {
			return fmt.Errorf("imaged: job %s materialization failed after %d attempts: %s", job.ID, updated.ImageMaterializationAttempts, reason)
		}
		return fmt.Errorf("imaged: job %s materialization attempt %d failed; retry scheduled: %s", job.ID, updated.ImageMaterializationAttempts, reason)
	}
	if _, err := images.JobSetImageMaterialization(ctx, job.ID, job.ImageRef, "failed", "", "", reason); err != nil {
		return fmt.Errorf("imaged: job %s materialization failed (%s), recording failure: %w", job.ID, reason, err)
	}
	return fmt.Errorf("imaged: job %s materialization failed: %s", job.ID, reason)
}

func (h *Handler) jobMaterializationOwner() string {
	if h.nodeName != "" {
		return h.nodeName
	}
	return "imaged"
}

func jobMaterializationRetryDelay(attempt int) time.Duration {
	if attempt < 1 {
		attempt = 1
	}
	delay := jobMaterializationRetryBase
	for i := 1; i < attempt; i++ {
		delay *= 2
		if delay >= jobMaterializationRetryMax {
			return jobMaterializationRetryMax
		}
	}
	if delay > jobMaterializationRetryMax {
		return jobMaterializationRetryMax
	}
	return delay
}
