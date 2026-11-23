package scheddgrpc

import (
	"context"
	"testing"

	scheddpb "github.com/onebox-faas/faas/api/proto/onebox/faas/schedd/v1"
	"github.com/onebox-faas/faas/pkg/sched"
)

func TestNoteStartupOOMMarksOnlyFlaggedInstances(t *testing.T) {
	rec := &sched.StartupOOMRecorder{}
	ctx := sched.WithStartupOOMRecorder(context.Background(), rec)
	noteStartupOOM(ctx, &scheddpb.AdmitInstanceResponse{InstanceId: "oom", StartupOom: true})
	noteStartupOOM(ctx, &scheddpb.EnsureWakeResponse{InstanceId: "healthy"})
	if !rec.Has("oom") || rec.Has("healthy") {
		t.Fatalf("recorder: oom=%v healthy=%v, want true false", rec.Has("oom"), rec.Has("healthy"))
	}
}
