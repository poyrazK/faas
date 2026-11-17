package imaged

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// Image assembly used to show only the coarse image_build/imaging stage:
// a deployment sat there for minutes without saying which image was being
// converted or whether it was still moving (Kroki feedback 2026-11-17).
// imaged now writes one deployment-log line per step and workload, on the
// "image" stream, which `gregale deploy` tails live.

// imageProgressStream is the deployment_logs stream imaged writes to.
const imageProgressStream = "image"

type deploymentLogAppender interface {
	AppendDeploymentLog(ctx context.Context, deploymentID, stream, line string) (int64, error)
}

type imageProgressKey struct{}

type imageProgress struct {
	store    deploymentLogAppender
	depID    string
	workload string
}

// withImageProgress makes later steps in ctx report on workload ("app image"
// or `companion "name"`) of deployment depID.
func withImageProgress(ctx context.Context, store deploymentLogAppender, depID, workload string) context.Context {
	if store == nil || depID == "" {
		return ctx
	}
	return context.WithValue(ctx, imageProgressKey{}, imageProgress{store: store, depID: depID, workload: workload})
}

// imageProgressf writes one progress line for ctx's workload. Best effort:
// progress must never fail a deployment.
func imageProgressf(ctx context.Context, format string, args ...any) {
	p, ok := ctx.Value(imageProgressKey{}).(imageProgress)
	if !ok {
		return
	}
	line := fmt.Sprintf("[image] %s: %s", p.workload, strings.TrimSpace(fmt.Sprintf(format, args...)))
	_, _ = p.store.AppendDeploymentLog(context.WithoutCancel(ctx), p.depID, imageProgressStream, line+"\n")
}

func progressMB(b int64) string {
	return fmt.Sprintf("%.1f MB", float64(b)/(1024*1024))
}

func progressElapsed(start time.Time) string {
	return time.Since(start).Round(100 * time.Millisecond).String()
}

func companionWorkload(name string) string {
	return fmt.Sprintf("companion %q", name)
}
