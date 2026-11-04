package sched

import (
	"context"
	"log/slog"
	"time"

	"google.golang.org/grpc/status"

	"github.com/onebox-faas/faas/pkg/logsanitize"
)

// logVMMBootFailure records a failed vmmd boot RPC with what it takes to tell
// who ended it. production-us hunt #14 (H14-82): vmmd reported "readiness:
// context canceled" 340 ms into a cold boot, and schedd kept only the
// wake.boot_failed event, so nothing showed whether schedd's own boot context
// had ended (client cancel or deadline) or vmmd stopped the boot itself.
// boot_ctx_err is empty when schedd's context was still live; grpc_code is
// the status vmmd returned (Canceled with "the client connection is closing"
// is a connection torn down under the call).
func logVMMBootFailure(log *slog.Logger, bootCtx context.Context, appID, instanceID, wakeID, nodeID, method string, rpc time.Duration, err error) {
	if log == nil || err == nil {
		return
	}
	log.Warn("wake: vmmd boot failed",
		"app", logsanitize.Field(appID), "instance", instanceID, "wake_id", logsanitize.Field(wakeID),
		"node", nodeID, "method", method, "rpc_ms", rpc.Milliseconds(),
		"grpc_code", status.Code(err).String(),
		"boot_ctx_err", errText(bootCtx.Err()), "boot_ctx_cause", errText(context.Cause(bootCtx)),
		"err", err)
}

func errText(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}
