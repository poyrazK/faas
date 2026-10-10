package main

import (
	"context"
	"log/slog"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/fcvm"
)

const (
	nativeArtifactRetirementRecoveryInterval = time.Minute
	nativeArtifactRetirementRecoveryTimeout  = 30 * time.Second
)

type nativeArtifactRetirementRecoverer interface {
	RecoverNativeQualificationArtifactRetirementPage(context.Context, string, int) (fcvm.NativeQualificationArtifactRetirementPage, error)
}

// runNativeArtifactRetirementRecovery retries only durable, already-authorized
// artifact retirements. Startup performs the first recovery before vmmd serves;
// this loop covers later storage outages without granting new cleanup authority.
func runNativeArtifactRetirementRecovery(ctx context.Context, manager nativeArtifactRetirementRecoverer, interval time.Duration, log *slog.Logger) {
	if manager == nil || log == nil {
		return
	}
	if interval <= 0 {
		interval = nativeArtifactRetirementRecoveryInterval
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	after := ""
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			attemptCtx, cancel := context.WithTimeout(ctx, nativeArtifactRetirementRecoveryTimeout)
			page, err := manager.RecoverNativeQualificationArtifactRetirementPage(attemptCtx, after, api.NativeSnapshotPublicationRecoveryBatchMax)
			cancel()
			previous := after
			if !page.More || page.NextCursor == "" || page.NextCursor == previous {
				after = ""
			} else {
				after = page.NextCursor
			}
			if err != nil && ctx.Err() == nil {
				log.Warn("vmmd: native artifact retirement retry failed", "err", err, "examined", page.Examined, "next_cursor", page.NextCursor, "more", page.More)
			}
		}
	}
}
