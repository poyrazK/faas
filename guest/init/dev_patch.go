package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"log/slog"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/devpatch"
)

// ADR-740 developer live patches. guest-init asks vmmd for the newest patch
// of its own deployment, writes it into the application directory, and
// restarts the workload. The loop ends for good when vmmd says delivery is
// disabled, which is the answer for every non-developer app.

const (
	devPatchPollInterval = time.Second
	devPatchMaxBackoff   = 30 * time.Second
)

// devPatchWire mirrors cmd/vmmd's runtimeDevPatch.
type devPatchWire struct {
	Generation int64    `json:"generation"`
	ImageDir   string   `json:"image_dir"`
	Archive    []byte   `json:"archive"`
	Deleted    []string `json:"deleted,omitempty"`
	Digest     string   `json:"digest"`
}

type devPatchPoll struct {
	Patch     *devPatchWire
	Unchanged bool
	Error     string
}

type devPatchIO struct {
	fetch   func(afterGeneration int64) (devPatchPoll, error)
	apply   func(dir string, archive []byte, deleted []string) (devpatch.ApplyResult, error)
	restart func() error
	sleep   func(context.Context, time.Duration) bool
	// ack reports the outcome of one generation to vmmd; errorCode is empty
	// on success. It is best-effort and never blocks polling.
	ack func(generation, applyMS int64, errorCode string)
}

// devPatchStopErrors end polling: delivery is off, the app is not a
// developer app, or vmmd predates ADR-740 and rejects the request kind.
var devPatchStopErrors = map[string]bool{
	"dev_patch_disabled": true,
	"invalid_request":    true,
	"unsupported_scope":  true,
}

func runDevPatchLoop(ctx context.Context, log *slog.Logger, io devPatchIO) {
	var generation int64
	failures := 0
	for {
		delay := devPatchPollInterval
		poll, err := io.fetch(generation)
		switch {
		case err != nil || (poll.Error != "" && !devPatchStopErrors[poll.Error]):
			failures++
			delay = devPatchBackoff(failures)
		case poll.Error != "":
			log.Debug("developer live patches disabled", "reason", poll.Error)
			return
		case poll.Patch != nil:
			failures = 0
			// A patch that cannot be applied is skipped rather than retried
			// every second; the next developer sync publishes a newer one.
			generation = poll.Patch.Generation
			applyDevPatch(log, io, poll.Patch)
		default:
			failures = 0
		}
		if !io.sleep(ctx, delay) {
			return
		}
	}
}

func applyDevPatch(log *slog.Logger, io devPatchIO, patch *devPatchWire) {
	sum := sha256.Sum256(patch.Archive)
	if patch.ImageDir != api.DevPatchImageDir || hex.EncodeToString(sum[:]) != patch.Digest {
		log.Warn("developer live patch rejected", "generation", patch.Generation, "reason", "invalid_patch")
		io.ack(patch.Generation, 0, "invalid_patch")
		return
	}
	started := time.Now()
	result, err := io.apply(patch.ImageDir, patch.Archive, patch.Deleted)
	if err != nil {
		log.Warn("developer live patch failed", "generation", patch.Generation, "written", result.Written, "err", err)
		io.ack(patch.Generation, time.Since(started).Milliseconds(), "apply_failed")
		return
	}
	if err := io.restart(); err != nil {
		log.Warn("developer live patch applied but restart failed", "generation", patch.Generation, "err", err)
		io.ack(patch.Generation, time.Since(started).Milliseconds(), "restart_failed")
		return
	}
	io.ack(patch.Generation, time.Since(started).Milliseconds(), "")
	log.Info("developer live patch applied", "generation", patch.Generation,
		"written", result.Written, "deleted", result.Deleted, "bytes", result.Bytes,
		"apply_ms", time.Since(started).Milliseconds())
}

func devPatchBackoff(failures int) time.Duration {
	delay := devPatchPollInterval << min(failures, 5)
	return min(delay, devPatchMaxBackoff)
}
