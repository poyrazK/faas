package builderd

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"strings"
)

// BuildEnvironment identifies the builder toolchain and output platform used
// to produce a cached artifact. The identity hashes the staged builder base's
// complete digest sidecar, which includes the OCI config, base layout, and
// injected guest-init digest.
type BuildEnvironment struct {
	BuilderBaseIdentity string `json:"builder_base_identity"`
	TargetPlatform      string `json:"target_platform"`
}

type buildEnvironmentProvider interface {
	BuildEnvironment() (BuildEnvironment, error)
}

func currentBuildEnvironment(vm VM) (BuildEnvironment, error) {
	provider, ok := vm.(buildEnvironmentProvider)
	if !ok {
		return BuildEnvironment{}, errors.New("VM driver does not expose a build environment identity")
	}
	environment, err := provider.BuildEnvironment()
	if err != nil {
		return BuildEnvironment{}, err
	}
	environment.BuilderBaseIdentity = strings.TrimSpace(environment.BuilderBaseIdentity)
	environment.TargetPlatform = strings.TrimSpace(environment.TargetPlatform)
	if environment.BuilderBaseIdentity == "" {
		return BuildEnvironment{}, errors.New("VM driver returned an empty builder base identity")
	}
	if environment.TargetPlatform == "" {
		return BuildEnvironment{}, errors.New("VM driver returned an empty target platform")
	}
	return environment, nil
}

// readBuildEnvironment reads the small sidecar rather than hashing the full
// builder ext4 for every build. imaged publishes the base first and the
// sidecar second; an older sidecar mtime therefore means staging is in flight
// or was interrupted, so cache reuse must wait.
func readBuildEnvironment(builderBase, platform string) (BuildEnvironment, error) {
	builderBase = strings.TrimSpace(builderBase)
	platform = strings.TrimSpace(platform)
	if builderBase == "" {
		return BuildEnvironment{}, errors.New("builder base path is empty")
	}
	if platform == "" {
		return BuildEnvironment{}, errors.New("target platform is empty")
	}

	baseInfo, err := os.Stat(builderBase)
	if err != nil {
		return BuildEnvironment{}, fmt.Errorf("stat builder base: %w", err)
	}
	if !baseInfo.Mode().IsRegular() || baseInfo.Size() == 0 {
		return BuildEnvironment{}, errors.New("builder base is not a non-empty regular file")
	}

	sidecarPath := builderBase + ".digest"
	sidecarInfo, err := os.Stat(sidecarPath)
	if err != nil {
		return BuildEnvironment{}, fmt.Errorf("stat builder base digest sidecar: %w", err)
	}
	if !sidecarInfo.Mode().IsRegular() || sidecarInfo.Size() == 0 {
		return BuildEnvironment{}, errors.New("builder base digest sidecar is not a non-empty regular file")
	}
	if sidecarInfo.ModTime().Before(baseInfo.ModTime()) {
		return BuildEnvironment{}, errors.New("builder base digest sidecar predates the builder base")
	}

	data, err := os.ReadFile(sidecarPath)
	if err != nil {
		return BuildEnvironment{}, fmt.Errorf("read builder base digest sidecar: %w", err)
	}
	identitySource := strings.TrimSpace(string(data))
	lines := strings.Split(identitySource, "\n")
	if len(lines) < 2 || !validSHA256Digest(strings.TrimSpace(lines[0])) || strings.TrimSpace(lines[1]) == "" {
		return BuildEnvironment{}, errors.New("builder base digest sidecar has an invalid identity")
	}

	sum := sha256.Sum256([]byte(identitySource))
	return BuildEnvironment{
		BuilderBaseIdentity: "sha256:" + hex.EncodeToString(sum[:]),
		TargetPlatform:      platform,
	}, nil
}

func validSHA256Digest(value string) bool {
	const prefix = "sha256:"
	if !strings.HasPrefix(value, prefix) || len(value) != len(prefix)+sha256.Size*2 {
		return false
	}
	_, err := hex.DecodeString(value[len(prefix):])
	return err == nil
}
