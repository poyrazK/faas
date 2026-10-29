package imaged

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/rootfs"
	"github.com/onebox-faas/faas/pkg/sched"
	"github.com/onebox-faas/faas/pkg/storage"
	"github.com/onebox-faas/faas/pkg/wire"
)

// agentLayerBuilder records each platform layer it publishes, with the
// staging tree's marker as it stood at mkfs time.
type agentLayerBuilder struct {
	callCountingBuilder
	published []string
	marker    string
	label     string
	sizeMB    int
}

func (b *agentLayerBuilder) PublishStagedLayer(ctx context.Context, be storage.StorageBackend, key, staging, label string, sizeMB int) (rootfs.ArtifactIdentity, error) {
	marker, err := os.ReadFile(filepath.Join(staging, "upper", "etc", "faas", "agent.json"))
	if err != nil {
		return rootfs.ArtifactIdentity{}, err
	}
	b.published, b.marker, b.label, b.sizeMB = append(b.published, key), string(marker), label, sizeMB
	return rootfs.ArtifactIdentity{Digest: "sha256:layer", Bytes: 4}, be.Put(ctx, key, strings.NewReader("ext4"))
}

// adr: 20261027090010 — an agent-pool imaged stages agent-base with its scan sidecar
// and the platform agent layer, only when the base is configured and pinned.
func TestAgentRunDrivesStageOnlyWhenConfigured(t *testing.T) {
	ctx := context.Background()
	puller := newTwoLayerPuller(t)
	builder := &agentLayerBuilder{}
	hs := newBaseHarness(t, puller, builder)
	var chowned []string
	hs.h.lchown = func(path string, uid, gid int) error {
		if uid != 0 || gid != 0 {
			t.Errorf("chown %s to %d:%d, want root", path, uid, gid)
		}
		chowned = append(chowned, path)
		return nil
	}
	env := map[string]string{}
	lookup := func(key string) string { return env[key] }
	if got, err := hs.h.EnsureAgentRunDrives(ctx, "amd64", lookup); err != nil || got != nil || puller.manifestCalls != 0 {
		t.Fatalf("unconfigured agent base staged: %+v, %v", got, err)
	}
	env[AgentBaseRefEnv] = "ghcr.io/poyrazk/agent-base:latest"
	if _, err := hs.h.EnsureAgentRunDrives(ctx, "amd64", lookup); err == nil || puller.manifestCalls != 0 {
		t.Fatal("unpinned agent base fetched an image")
	}
	env[AgentBaseRefEnv] = "ghcr.io/poyrazk/agent-base@sha256:" + strings.Repeat("b", 64)
	if _, err := hs.h.EnsureAgentRunDrives(ctx, "arm64", lookup); err == nil || puller.manifestCalls != 0 {
		t.Fatal("unsupported architecture fetched an image")
	}

	got, err := hs.h.EnsureAgentRunDrives(ctx, "amd64", lookup)
	if err != nil || len(got) != 1 || got[0].Runtime != sched.AgentRunBaseRuntime || got[0].Skipped {
		t.Fatalf("agent base staging = %+v, %v", got, err)
	}
	baseKey := sched.BaseKeyForArch(sched.AgentRunBaseRuntime, "amd64")
	if baseKey != "base/runner-agent-amd64.ext4" {
		t.Fatalf("agent base key = %q", baseKey)
	}
	for _, key := range []string{baseKey, wire.ScanKeyForBaseKey(baseKey), sched.AgentRunLayerKeyForArch("amd64")} {
		rc, err := hs.be.Get(ctx, key)
		if err != nil {
			t.Fatalf("%s not published: %v", key, err)
		}
		_ = rc.Close()
	}
	scan, err := readScanSidecar(ctx, hs.be, wire.ScanKeyForBaseKey(baseKey))
	if err != nil || !strings.Contains(scan.Image, "agent-base@sha256:") {
		t.Fatalf("scan sidecar = %+v, %v; want it to name the pinned agent base", scan, err)
	}
	if builder.marker != agentManifestJSON || builder.label != "agent-layer" || builder.sizeMB != agentLayerMB {
		t.Fatalf("agent layer marker %q label %q size %d", builder.marker, builder.label, builder.sizeMB)
	}
	if len(chowned) != 4 || !strings.HasSuffix(chowned[3], filepath.Join("upper", "etc", "faas", "agent.json")) {
		t.Fatalf("chowned %q, want upper, etc, faas and the marker", chowned)
	}

	got, err = hs.h.EnsureAgentRunDrives(ctx, "amd64", lookup)
	if err != nil || len(got) != 1 || !got[0].Skipped || len(builder.published) != 1 {
		t.Fatalf("restage = %+v, %v with %d layer publications; want both reused", got, err, len(builder.published))
	}
}

// adr: 20261027090010 — a builder that cannot publish platform layers fails the
// staging instead of leaving agent runs without drive1.
func TestAgentRunDrivesNeedALayerPublisher(t *testing.T) {
	hs := newBaseHarness(t, newTwoLayerPuller(t), &callCountingBuilder{})
	hs.h.lchown = func(string, int, int) error { return nil }
	env := map[string]string{AgentBaseRefEnv: "ghcr.io/poyrazk/agent-base@sha256:" + strings.Repeat("c", 64)}
	if _, err := hs.h.EnsureAgentRunDrives(context.Background(), "amd64", func(key string) string { return env[key] }); err == nil {
		t.Fatal("agent drives staged without a layer publisher")
	}
}

type scanSidecarImage struct {
	Image string `json:"image"`
}

func readScanSidecar(ctx context.Context, be storage.StorageBackend, key string) (scanSidecarImage, error) {
	var out scanSidecarImage
	rc, err := be.Get(ctx, key)
	if err != nil {
		return out, err
	}
	defer func() { _ = rc.Close() }()
	body, err := io.ReadAll(rc)
	if err != nil {
		return out, err
	}
	return out, json.Unmarshal(body, &out)
}
