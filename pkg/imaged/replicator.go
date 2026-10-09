package imaged

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strings"

	"github.com/onebox-faas/faas/pkg/cosign"
	"github.com/onebox-faas/faas/pkg/safetext"
)

// replicatorHelperDetailMaxBytes bounds the replication helper's combined
// output when it is folded into an error message.
const replicatorHelperDetailMaxBytes = 2048

// CommandArtifactReplicator adapts an operator-owned artifact handoff helper
// to ArtifactReplicator. The helper receives exactly two positional
// arguments: the layer key and its derived signature key, or
// --runtime-release and an immutable base key. Keeping the
// transfer policy outside the daemon lets a local split-box install use SSH,
// while an OCI deployment leaves the hook unset and uses the shared backend.
type CommandArtifactReplicator struct {
	Path     string
	ExtraEnv []string
}

// RuntimeReleaseReplicator hands off immutable drive0 bytes and their scan
// evidence before an application artifact can bind to them (ADR-736).
type RuntimeReleaseReplicator interface {
	ReplicateRuntimeRelease(context.Context, string) error
}

var runtimeReleaseKey = regexp.MustCompile(`^base/releases/runner-(node22|node24|python312|python313|go124|go124-alpine)-(amd64|arm64)-[a-f0-9]{64}\.ext4$`)

func (r CommandArtifactReplicator) ReplicateRuntimeRelease(ctx context.Context, key string) error {
	if !runtimeReleaseKey.MatchString(key) {
		return fmt.Errorf("invalid immutable runtime base key")
	}
	return r.run(ctx, "--runtime-release", key)
}

// Replicate runs the configured helper with a cancellable context. Helper
// output is included only on failure and is capped so a broken transport
// cannot flood imaged's error log.
func (r CommandArtifactReplicator) Replicate(ctx context.Context, layerKey string) error {
	if layerKey == "" {
		return fmt.Errorf("empty layer key")
	}
	return r.run(ctx, layerKey, cosign.SigKeyFor(layerKey))
}

func (r CommandArtifactReplicator) run(ctx context.Context, args ...string) error {
	if r.Path == "" {
		return fmt.Errorf("empty artifact replicator path")
	}
	cmd := exec.CommandContext(ctx, r.Path, args...)
	if len(r.ExtraEnv) > 0 {
		cmd.Env = append(os.Environ(), r.ExtraEnv...)
	}
	out, err := cmd.CombinedOutput()
	if err == nil {
		return nil
	}
	detail := safetext.Ellipsis(strings.TrimSpace(string(out)), replicatorHelperDetailMaxBytes)
	if detail == "" {
		return fmt.Errorf("helper %q: %w", r.Path, err)
	}
	return fmt.Errorf("helper %q: %w: %s", r.Path, err, detail)
}
