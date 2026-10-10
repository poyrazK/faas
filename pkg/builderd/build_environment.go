package builderd

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
)

// BuildEnvironment identifies the builder toolchain and output platform used
// to produce a cached artifact. The identity covers what can change a build's
// output: the builder image (its OCI config digest), the base layout, and
// guest-init's build contract (builderIdentity).
type BuildEnvironment struct {
	BuilderBaseIdentity string `json:"builder_base_identity"`
	BaseDigest          string `json:"base_digest"`
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
	environment.BaseDigest = strings.TrimSpace(environment.BaseDigest)
	environment.TargetPlatform = strings.TrimSpace(environment.TargetPlatform)
	if environment.BuilderBaseIdentity == "" {
		return BuildEnvironment{}, errors.New("VM driver returned an empty builder base identity")
	}
	if environment.TargetPlatform == "" {
		return BuildEnvironment{}, errors.New("VM driver returned an empty target platform")
	}
	return environment, nil
}

// ReadBuildEnvironment validates and reads the staged builder-base identity
// from a host path, deriving the digest sidecar as the base's sibling. It is
// exported for daemon readiness checks so builderd does not advertise capacity
// while its drive0 or digest sidecar is missing. The validation is the same one
// used by the build-cache lookup path.
//
// Only correct when the base really is a plain file in the storage root. Under
// an OCI backend it is not — use ReadBuildEnvironmentAt.
func ReadBuildEnvironment(builderBase, platform string) (BuildEnvironment, error) {
	return readBuildEnvironment(builderBase, "", platform)
}

// ReadBuildEnvironmentAt is ReadBuildEnvironment with the digest sidecar's
// location supplied by the caller instead of derived from the base path.
//
// The derivation (base + ".digest") only holds for the local backend, where
// the base is /srv/fc/base/runner-builder-<arch>.ext4 and the sidecar is
// genuinely its sibling. Under the OCI backend, builderd resolves the base
// through storage.LocalPathResolver into the read-through cache, which is
// content-addressed: the base lands at something like
// /var/lib/faas/cache/e6/572dbb..., and no sibling ".digest" exists there or
// ever will — the sidecar is a separate storage key
// (sched.BaseDigestKeyForArch). Deriving it there made every build on an
// OCI-backed node fail with "stat builder base digest sidecar: no such file
// or directory", reported as failure_class=user_error.
//
// Passing an empty digestPath keeps the sibling derivation.
func ReadBuildEnvironmentAt(builderBase, digestPath, platform string) (BuildEnvironment, error) {
	return readBuildEnvironment(builderBase, digestPath, platform)
}

// readBuildEnvironment reads the small sidecar rather than hashing the full
// builder ext4 for every build. imaged publishes the base first and the
// sidecar second; an older sidecar mtime therefore means staging is in flight
// or was interrupted, so cache reuse must wait.
func readBuildEnvironment(builderBase, digestPath, platform string) (BuildEnvironment, error) {
	builderBase = strings.TrimSpace(builderBase)
	digestPath = strings.TrimSpace(digestPath)
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

	// derivedSidecar records whether the two paths are siblings, which is what
	// makes the mtime ordering check below meaningful.
	derivedSidecar := digestPath == ""
	sidecarPath := digestPath
	if derivedSidecar {
		sidecarPath = builderBase + ".digest"
	}
	sidecarInfo, err := os.Stat(sidecarPath)
	if err != nil {
		return BuildEnvironment{}, fmt.Errorf("stat builder base digest sidecar: %w", err)
	}
	if !sidecarInfo.Mode().IsRegular() || sidecarInfo.Size() == 0 {
		return BuildEnvironment{}, errors.New("builder base digest sidecar is not a non-empty regular file")
	}
	// The ordering check catches staging that was interrupted between
	// publishing the base and publishing its sidecar. It is only valid when
	// both files were written by that staging pass — i.e. when they are
	// siblings in the storage root. In a read-through cache the mtimes record
	// when each blob happened to be pulled, which has no relation to
	// publication order, so a cache-resolved sidecar would fail this check at
	// random. There the commit marker is the cached base generation
	// (markCachedBaseGeneration), which imaged writes only after the base,
	// digest and scan sidecars are all present.
	if derivedSidecar && sidecarInfo.ModTime().Before(baseInfo.ModTime()) {
		return BuildEnvironment{}, errors.New("builder base digest sidecar predates the builder base")
	}

	data, err := os.ReadFile(sidecarPath)
	if err != nil {
		return BuildEnvironment{}, fmt.Errorf("read builder base digest sidecar: %w", err)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	baseDigest, layout := strings.TrimSpace(lines[0]), ""
	if len(lines) >= 2 {
		layout = strings.TrimSpace(lines[1])
	}
	if !validSHA256Digest(baseDigest) || layout == "" {
		return BuildEnvironment{}, errors.New("builder base digest sidecar has an invalid identity")
	}

	return BuildEnvironment{
		BuilderBaseIdentity: builderIdentity(baseDigest, layout),
		BaseDigest:          baseDigest,
		TargetPlatform:      platform,
	}, nil
}

// builderIdentity derives the cache identity from the builder image, the base
// layout and guest-init's build contract version. The sidecar's remaining
// lines are deliberately left out: the guest-init binary digest changes on
// every release because guest-init links most of pkg/api, and keying on it
// started every release with a cold cache (gate builds took 5 min instead
// of seconds). The source ref names the same image as the config digest.
func builderIdentity(baseDigest, layout string) string {
	sum := sha256.Sum256([]byte(baseDigest + "\n" + layout + "\nguest-init-build-contract=" +
		strconv.Itoa(guestInitBuildContractVersion)))
	return "sha256:" + hex.EncodeToString(sum[:])
}

func validSHA256Digest(value string) bool {
	const prefix = "sha256:"
	if !strings.HasPrefix(value, prefix) || len(value) != len(prefix)+sha256.Size*2 {
		return false
	}
	_, err := hex.DecodeString(value[len(prefix):])
	return err == nil
}
