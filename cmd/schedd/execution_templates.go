package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/sched"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/storage"
)

// Template warmup is an explicit release rollout switch. Cold Run admission
// never waits for it; absence of a ready catalog row remains a cold boot.
func startExecutionTemplates(ctx context.Context, log *slog.Logger, store *state.PgStore, backend storage.StorageBackend, router *sched.VMMRouter, nodeID string, artifacts sched.ExecutionRuntimeArtifactsResolver) error {
	if os.Getenv("FAAS_EXECUTION_TEMPLATE_CAPTURE") != "1" {
		return nil
	}
	if kind := strings.TrimSpace(os.Getenv("FAAS_STORAGE_BACKEND")); kind != "" && kind != "local" {
		return errors.New("schedd: execution template capture currently requires the local artifact backend")
	}
	accountID := strings.TrimSpace(os.Getenv("FAAS_EXECUTION_TEMPLATE_ACCOUNT_ID"))
	if uuid.Validate(accountID) != nil {
		return errors.New("schedd: template capture requires a dedicated platform account UUID")
	}
	shapes, err := executionTemplateShapes(os.Getenv("FAAS_EXECUTION_TEMPLATE_SHAPES"))
	if err != nil {
		return err
	}
	if account, err := store.AccountByID(ctx, accountID); err != nil || !account.Active() || account.Plan != api.PlanScale {
		return errors.New("schedd: execution template account must be an active platform Scale account")
	}
	specs := make([]sched.ExecutionTemplateSpec, 0, len(shapes))
	for _, shape := range shapes {
		a, err := artifacts.ResolveExecutionArtifacts(ctx, shape.Runtime, shape)
		if err != nil {
			return fmt.Errorf("schedd: resolve template release: %w", err)
		}
		identity := sched.RuntimeSnapshotRequest{FCVersion: a.FCVersion, Shape: shape, Architecture: a.Architecture, KernelDigest: a.KernelDigest, GuestExecutorDigest: a.GuestExecutorDigest, BaseImageDigest: a.BaseImageDigest, FormatVersion: sched.CurrentRuntimeSnapshotFormatVersion}.Identity()
		if err := identity.Validate(); err != nil {
			return err
		}
		specs = append(specs, sched.ExecutionTemplateSpec{AccountID: accountID, NodeID: nodeID, Identity: identity, Artifacts: a})
	}
	publisher := sched.NewExecutionTemplatePublisher(store, backend, router)
	go func() {
		ticker := time.NewTicker(time.Minute)
		defer ticker.Stop()
		for {
			for _, spec := range specs {
				if ctx.Err() != nil {
					return
				}
				account, err := store.AccountByID(ctx, accountID)
				if err != nil || !account.Active() || account.Plan != api.PlanScale {
					log.Warn("schedd: template platform account unavailable")
					break
				}
				entry, err := publisher.Ensure(ctx, spec)
				if err != nil {
					log.Warn("schedd: execution template publication failed", "runtime", spec.Identity.Runtime, "err", err)
					continue
				}
				log.Info("schedd: execution template ready", "runtime", entry.Identity.Runtime, "snapshot_digest", entry.SnapshotDigest, "storage_key", entry.StorageKey)
			}
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	}()
	return nil
}

func executionTemplateShapes(raw string) ([]api.ExecutionSnapshotShape, error) {
	if len(raw) > api.ExecutionTemplateShapesMaxBytes {
		return nil, errors.New("schedd: template shape configuration is too large")
	}
	decoder := json.NewDecoder(strings.NewReader(raw))
	decoder.DisallowUnknownFields()
	var shapes []api.ExecutionSnapshotShape
	if err := decoder.Decode(&shapes); err != nil {
		return nil, fmt.Errorf("schedd: decode template shapes: %w", err)
	}
	if len(shapes) == 0 || len(shapes) > api.ExecutionTemplateShapesMax {
		return nil, errors.New("schedd: template shape count is invalid")
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return nil, errors.New("schedd: template shapes contain trailing data")
	}
	seen := map[api.ExecutionSnapshotShape]bool{}
	for _, shape := range shapes {
		shape.Profile = shape.Profile.Normalized()
		if err := shape.Profile.Validate(shape.Runtime); err != nil {
			return nil, err
		}
		if !shape.Runtime.Valid() || !api.ValidExecutionMemoryMB(shape.MemoryMB) || !api.ValidExecutionEphemeralDiskMB(shape.EphemeralDiskMB) || seen[shape] {
			return nil, errors.New("schedd: invalid or repeated template shape")
		}
		seen[shape] = true
	}
	return shapes, nil
}
