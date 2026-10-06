package imaged

import (
	"context"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/sched"
)

func TestExecutionProfileBaseStagingIsOptInAndSeparate(t *testing.T) {
	ctx := context.Background()
	puller := newTwoLayerPuller(t)
	hs := newBaseHarness(t, puller, &callCountingBuilder{})
	env := map[string]string{}
	lookup := func(key string) string { return env[key] }
	if got, err := hs.h.EnsureExecutionProfileBases(ctx, "amd64", lookup); err != nil || len(got) != 0 || puller.manifestCalls != 0 {
		t.Fatalf("unconfigured profile fetched image: %+v, %v", got, err)
	}
	env[PythonDataBaseRefEnv] = "ghcr.io/poyrazk/execution-python-data-v1:latest"
	if _, err := hs.h.EnsureExecutionProfileBases(ctx, "amd64", lookup); err == nil || puller.manifestCalls != 0 {
		t.Fatal("unpinned profile fetched image")
	}
	env[PythonDataBaseRefEnv] = "ghcr.io/poyrazk/execution-python-data-v1@sha256:" + strings.Repeat("a", 64)
	if _, err := hs.h.EnsureExecutionProfileBases(ctx, "arm64", lookup); err == nil || puller.manifestCalls != 0 {
		t.Fatal("unsupported architecture fetched image")
	}
	got, err := hs.h.EnsureExecutionProfileBases(ctx, "amd64", lookup)
	if err != nil || len(got) != 1 || got[0].Runtime != "python-data-v1" {
		t.Fatalf("profile staging=%+v, %v", got, err)
	}
	if _, err := hs.be.Get(ctx, sched.BaseKeyForArch("python-data-v1", "amd64")); err != nil {
		t.Fatal(err)
	}
	if _, err := hs.be.Get(ctx, sched.BaseKeyForArch("python313", "amd64")); err == nil {
		t.Fatal("profile overwrote standard runtime base")
	}
	got, err = hs.h.EnsureExecutionProfileBases(ctx, "amd64", lookup)
	if err != nil || len(got) != 1 || !got[0].Skipped {
		t.Fatalf("shared profile base restaged: %+v, %v", got, err)
	}
}
