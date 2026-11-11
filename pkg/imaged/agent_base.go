package imaged

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/onebox-faas/faas/pkg/rootfs"
	"github.com/onebox-faas/faas/pkg/storage"
	"github.com/onebox-faas/faas/pkg/storagekey"
)

// AgentBaseRefEnv names the digest-pinned linux/amd64 agent-base image
// (images/agent-base.Dockerfile) an agent-pool imaged stages (ADR-20261027090010 §2).
const AgentBaseRefEnv = "FAAS_AGENT_BASE_REF"

const (
	agentLayerLabel = "agent-layer"
	// agentLayerMB sizes drive1. The marker is its only file; the rest is the
	// run's scratch (the checkout's tree and outputs) until runs get a
	// plan-sized scratch drive (ADR-20261027090010 §3).
	agentLayerMB = 512
	// agentManifestJSON selects guest-init's agent mode (guest/init/agent_run.go).
	agentManifestJSON = `{"kind":"agent","version":1}`
	// sandboxLayerLabel and sandboxManifestJSON are the sandbox session
	// layer's (guest/init/sandbox.go, ADR-20261027090013 §2). Its workspace
	// lives on drive1 until sessions get a scratch drive.
	sandboxLayerLabel   = "sandbox-layer"
	sandboxManifestJSON = `{"kind":"sandbox","version":1}`
)

// platformLayer is one platform drive1: a single mode marker under
// upper/etc/faas.
type platformLayer struct {
	key, label, marker, content string
}

func agentRunLayer(arch string) platformLayer {
	return platformLayer{key: storagekey.AgentRunLayerForArch(arch), label: agentLayerLabel, marker: "agent.json", content: agentManifestJSON}
}

func sandboxLayer(arch string) platformLayer {
	return platformLayer{key: storagekey.SandboxLayerForArch(arch), label: sandboxLayerLabel, marker: "sandbox.json", content: sandboxManifestJSON}
}

// stagedLayerPublisher is implemented by *rootfs.Builder.
type stagedLayerPublisher interface {
	PublishStagedLayer(ctx context.Context, be storage.StorageBackend, key, staging, label string, sizeMB int) (rootfs.ArtifactIdentity, error)
}

var _ stagedLayerPublisher = (*rootfs.Builder)(nil)

// EnsureAgentRunDrives stages the agent run drives when AgentBaseRefEnv is
// set: drive0 through the digest-verified base path, which installs this
// release's guest-init and writes the Grype scan sidecar vmmd's admission
// gate reads (issue #299), then the platform agent and sandbox layers. Unset stages
// nothing, so nodes outside the agent pool carry no agent residency.
func (h *Handler) EnsureAgentRunDrives(ctx context.Context, arch string, envLookup func(string) string) ([]EnsureBasesResult, error) {
	if envLookup == nil {
		return nil, errors.New("imaged: agent base environment lookup is required")
	}
	ref := strings.TrimSpace(envLookup(AgentBaseRefEnv))
	if ref == "" {
		return nil, nil
	}
	if arch != "amd64" {
		return nil, errors.New("imaged: the agent base image requires amd64")
	}
	bases, err := h.EnsureBases(ctx, arch, []RuntimeBaseRef{{Runtime: storagekey.AgentRunBaseRuntime, Ref: ref, EnvOverride: AgentBaseRefEnv}}, envLookup)
	if err != nil {
		return nil, err
	}
	for _, layer := range []platformLayer{agentRunLayer(arch), sandboxLayer(arch)} {
		if err := h.ensurePlatformLayer(ctx, layer); err != nil {
			return nil, err
		}
	}
	return bases, nil
}

// ensurePlatformLayer publishes a platform layer unless it is already there.
// Its content is fixed by the key's version, so an existing publication is
// current.
func (h *Handler) ensurePlatformLayer(ctx context.Context, layer platformLayer) error {
	key := layer.key
	be, err := h.storageFor()
	if err != nil {
		return err
	}
	exists, supported, err := storage.Exists(ctx, be, key)
	if err != nil {
		return fmt.Errorf("imaged: check %s %s: %w", layer.label, key, err)
	}
	if !supported {
		if rc, getErr := be.Get(ctx, key); getErr == nil {
			_ = rc.Close()
			exists = true
		}
	}
	if exists {
		return nil
	}
	publisher, ok := h.builder.(stagedLayerPublisher)
	if !ok {
		return errors.New("imaged: the layer builder cannot publish platform layers")
	}
	staging, err := rootfs.MkdirBaseStaging()
	if err != nil {
		return fmt.Errorf("imaged: %s staging: %w", layer.label, err)
	}
	defer func() { _ = os.RemoveAll(staging) }()
	lchown := h.lchown
	if lchown == nil {
		lchown = os.Lchown
	}
	if err := writePlatformLayerTree(staging, layer, lchown); err != nil {
		return err
	}
	identity, err := publisher.PublishStagedLayer(ctx, be, key, staging, layer.label, agentLayerMB)
	if err != nil {
		return fmt.Errorf("imaged: publish %s %s: %w", layer.label, key, err)
	}
	h.log.Info("imaged platform layer published", "label", layer.label, "key", key, "digest", identity.Digest, "bytes", identity.Bytes)
	return nil
}

// writePlatformLayerTree lays out drive1 as guest-init's overlay expects:
// upper/etc/faas/<marker>, root-owned like the base's /etc it overlays.
// imaged runs unprivileged with CAP_CHOWN, as tar ownership already needs.
func writePlatformLayerTree(staging string, layer platformLayer, lchown func(string, int, int) error) error {
	upper := filepath.Join(staging, "upper")
	dirs := []string{upper, filepath.Join(upper, "etc"), filepath.Join(upper, "etc", "faas")}
	for _, dir := range dirs {
		if err := os.Mkdir(dir, 0o755); err != nil {
			return fmt.Errorf("imaged: %s %s: %w", layer.label, dir, err)
		}
		if err := os.Chmod(dir, 0o755); err != nil {
			return fmt.Errorf("imaged: %s mode %s: %w", layer.label, dir, err)
		}
	}
	marker := filepath.Join(dirs[2], layer.marker)
	if err := os.WriteFile(marker, []byte(layer.content), 0o644); err != nil {
		return fmt.Errorf("imaged: %s marker: %w", layer.label, err)
	}
	if err := os.Chmod(marker, 0o644); err != nil {
		return fmt.Errorf("imaged: %s marker mode: %w", layer.label, err)
	}
	for _, path := range append(dirs, marker) {
		if err := lchown(path, 0, 0); err != nil {
			return fmt.Errorf("imaged: %s ownership %s: %w", layer.label, path, err)
		}
	}
	return nil
}
