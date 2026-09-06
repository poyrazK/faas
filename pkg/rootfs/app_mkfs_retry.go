package rootfs

import (
	"context"
	"fmt"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
)

const appMkfsMaxAttempts = 4

// Filesystem metadata varies with mke2fs configuration. Grow only after an
// explicit filesystem block/inode allocation failure, never after host disk
// exhaustion, permission errors, or cancellation. Every attempted size remains
// inside the plan cap; callers publish and report only the completed size.
func (b *Builder) runAppMkfs(ctx context.Context, staging, output string, sizeMB int, limits api.Limits) (int, error) {
	sizeMB = max(MinLayerMB, sizeMB)
	if sizeMB > limits.AppLayerMaxMB {
		return 0, api.ErrAppLayerTooLarge(limits, int64(sizeMB)*mib)
	}
	for attempt := 0; attempt < appMkfsMaxAttempts; attempt++ {
		if err := ctx.Err(); err != nil {
			return 0, err
		}
		err := b.run.Run(ctx, MkfsCommand(staging, output, sizeMB))
		if err == nil {
			return sizeMB, nil
		}
		message := strings.ToLower(err.Error())
		if !strings.Contains(message, "could not allocate block") && !strings.Contains(message, "could not allocate inode") {
			return 0, fmt.Errorf("rootfs: mkfs: %w", err)
		}
		growth := max(slackFloorMB, sizeMB*perAppSlackPct/100)
		if sizeMB == limits.AppLayerMaxMB {
			return 0, api.ErrAppLayerTooLarge(limits, int64(sizeMB+1)*mib)
		}
		if attempt == appMkfsMaxAttempts-1 {
			return 0, fmt.Errorf("rootfs: mkfs exhausted %d size attempts at %d MiB: %w", appMkfsMaxAttempts, sizeMB, err)
		}
		sizeMB = min(sizeMB+growth, limits.AppLayerMaxMB)
	}
	panic("unreachable app mkfs retry loop")
}
