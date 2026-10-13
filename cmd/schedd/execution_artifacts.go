package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"regexp"
	goruntime "runtime"
	"strings"
	"sync"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/sched"
	"github.com/onebox-faas/faas/pkg/storage"
)

// executionBaseSidecarTTL bounds how long a base identity is reused before
// the sidecar is re-read, so a re-staged base generation is picked up and
// gets its own runtime snapshot identity.
const executionBaseSidecarTTL = 30 * time.Second

// executionArtifactFields are the per-runtime env override fields; when any
// of them is set for a runtime/profile, the explicit env metadata is used
// instead of the storage-derived identity.
var executionArtifactFields = []string{"ARCH", "KERNEL_DIGEST", "EXECUTOR_DIGEST", "BASE_DIGEST", "KERNEL_KEY", "BASE_KEY", "LAYER_KEY", "FC_VERSION"}

// storageExecutionArtifacts derives the execution machine from artifacts the
// release and imaged already publish: the Firecracker kernel for the running
// FC version, the runtime base and its digest sidecar (image config digest +
// guest-init digest; the one-shot executor is compiled into guest-init), and
// the payload-free execution layer imaged stages per profile.
type storageExecutionArtifacts struct {
	backend   storage.StorageBackend
	fcVersion string
	arch      string
	envLookup func(string) string
	now       func() time.Time

	mu      sync.Mutex
	kernels map[string]string
	bases   map[string]cachedBaseIdentity
}

type cachedBaseIdentity struct {
	configDigest string
	guestInit    string
	readAt       time.Time
}

func newStorageExecutionArtifacts(backend storage.StorageBackend, fcVersion, arch string, envLookup func(string) string) *storageExecutionArtifacts {
	return &storageExecutionArtifacts{
		backend: backend, fcVersion: strings.TrimSpace(fcVersion), arch: arch, envLookup: envLookup,
		now: time.Now, kernels: map[string]string{}, bases: map[string]cachedBaseIdentity{},
	}
}

// executionArtifactsResolver picks the production storage-derived resolver
// or the injected function (tests, explicit wiring).
func executionArtifactsResolver(deps runDeps, backend storage.StorageBackend, fcVersion string) sched.ExecutionRuntimeArtifactsFunc {
	if !deps.executionArtifactsFromStorage {
		return sched.ExecutionRuntimeArtifactsFunc(deps.executionArtifacts)
	}
	return newStorageExecutionArtifacts(backend, fcVersion, goruntime.GOARCH, os.Getenv).resolve
}

func (r *storageExecutionArtifacts) resolve(ctx context.Context, runtimeID api.ExecutionRuntime, shape api.ExecutionSnapshotShape) (sched.ExecutionRuntimeArtifacts, error) {
	if err := shape.Profile.Validate(runtimeID); err != nil {
		return sched.ExecutionRuntimeArtifacts{}, err
	}
	if r.envOverride(runtimeID, shape.Profile) {
		return executionRuntimeArtifactsFromEnv(ctx, runtimeID, shape)
	}
	if r.backend == nil {
		return sched.ExecutionRuntimeArtifacts{}, fmt.Errorf("%w: storage backend is not configured", sched.ErrExecutionRuntimeArtifactsUnavailable)
	}
	if r.fcVersion == "" {
		return sched.ExecutionRuntimeArtifacts{}, fmt.Errorf("%w: firecracker version is unknown", sched.ErrExecutionRuntimeArtifactsUnavailable)
	}
	profile := shape.Profile.Normalized()
	kernelKey := sched.KernelKey(r.fcVersion)
	kernelDigest, err := r.kernelDigest(ctx, kernelKey)
	if err != nil {
		return sched.ExecutionRuntimeArtifacts{}, err
	}
	baseRuntime := sched.ExecutionBaseRuntimeForProfile(string(runtimeID), string(profile))
	base, err := r.baseIdentity(ctx, baseRuntime)
	if err != nil {
		return sched.ExecutionRuntimeArtifacts{}, err
	}
	layerKey := sched.ExecutionLayerKeyForArch(string(profile), r.arch)
	if err := r.requireObject(ctx, sched.ExecutionLayerDigestKeyForArch(string(profile), r.arch)); err != nil {
		return sched.ExecutionRuntimeArtifacts{}, fmt.Errorf("%w: execution layer %s is not staged (imaged stages it at startup): %w",
			sched.ErrExecutionRuntimeArtifactsUnavailable, layerKey, err)
	}
	return sched.ExecutionRuntimeArtifacts{
		Profile: profile, Architecture: r.arch,
		KernelDigest: kernelDigest, GuestExecutorDigest: base.guestInit,
		BaseImageDigest: strings.TrimPrefix(base.configDigest, "sha256:"),
		KernelKey:       kernelKey, BaseKey: sched.BaseKeyForArch(baseRuntime, r.arch),
		LayerKey: layerKey, FCVersion: r.fcVersion,
	}, nil
}

func (r *storageExecutionArtifacts) envOverride(runtimeID api.ExecutionRuntime, profile api.ExecutionProfile) bool {
	if r.envLookup == nil {
		return false
	}
	prefix := "FAAS_EXECUTION_" + strings.ToUpper(string(runtimeID))
	if normalized := profile.Normalized(); normalized != api.ExecutionProfileStandard {
		prefix += "_" + strings.ToUpper(strings.ReplaceAll(string(normalized), "-", "_"))
	}
	for _, field := range executionArtifactFields {
		if strings.TrimSpace(r.envLookup(prefix+"_"+field)) != "" {
			return true
		}
	}
	return false
}

// kernelDigest hashes the kernel object once per key; kernel keys are
// version-pinned and therefore immutable.
func (r *storageExecutionArtifacts) kernelDigest(ctx context.Context, key string) (string, error) {
	r.mu.Lock()
	digest, ok := r.kernels[key]
	r.mu.Unlock()
	if ok {
		return digest, nil
	}
	rc, err := r.backend.Get(ctx, key)
	if err != nil {
		return "", fmt.Errorf("%w: kernel %s: %w", sched.ErrExecutionRuntimeArtifactsUnavailable, key, err)
	}
	defer func() { _ = rc.Close() }()
	hash := sha256.New()
	if _, err := io.Copy(hash, rc); err != nil {
		return "", fmt.Errorf("%w: hash kernel %s: %w", sched.ErrExecutionRuntimeArtifactsUnavailable, key, err)
	}
	digest = hex.EncodeToString(hash.Sum(nil))
	r.mu.Lock()
	r.kernels[key] = digest
	r.mu.Unlock()
	return digest, nil
}

func (r *storageExecutionArtifacts) baseIdentity(ctx context.Context, baseRuntime string) (cachedBaseIdentity, error) {
	sidecarKey := sched.BaseDigestKeyForArch(baseRuntime, r.arch)
	now := r.now()
	r.mu.Lock()
	cached, ok := r.bases[sidecarKey]
	r.mu.Unlock()
	if ok && now.Sub(cached.readAt) < executionBaseSidecarTTL {
		return cached, nil
	}
	rc, err := r.backend.Get(ctx, sidecarKey)
	if err != nil {
		return cachedBaseIdentity{}, fmt.Errorf("%w: base %s is not staged: %w", sched.ErrExecutionRuntimeArtifactsUnavailable, baseRuntime, err)
	}
	data, readErr := io.ReadAll(io.LimitReader(rc, 4096))
	_ = rc.Close()
	if readErr != nil {
		return cachedBaseIdentity{}, fmt.Errorf("%w: read base sidecar %s: %w", sched.ErrExecutionRuntimeArtifactsUnavailable, sidecarKey, readErr)
	}
	configDigest, guestInit, ok := parseBaseDigestSidecar(string(data))
	if !ok {
		return cachedBaseIdentity{}, fmt.Errorf("%w: base sidecar %s does not record a guest-init digest", sched.ErrExecutionRuntimeArtifactsUnavailable, sidecarKey)
	}
	identity := cachedBaseIdentity{configDigest: configDigest, guestInit: guestInit, readAt: now}
	r.mu.Lock()
	r.bases[sidecarKey] = identity
	r.mu.Unlock()
	return identity, nil
}

func (r *storageExecutionArtifacts) requireObject(ctx context.Context, key string) error {
	rc, err := r.backend.Get(ctx, key)
	if err != nil {
		return err
	}
	return rc.Close()
}

var sha256HexRE = regexp.MustCompile(`^[0-9a-f]{64}$`)

// parseBaseDigestSidecar reads the two identity lines imaged writes for a
// staged base (pkg/imaged base_stage.go): the image config digest on the
// first line and "guest-init-sha256=<hex>" further down. Anything else in
// the sidecar (layout version, source ref) is imaged's freshness contract and
// is not part of the Run identity.
func parseBaseDigestSidecar(sidecar string) (configDigest, guestInit string, ok bool) {
	lines := strings.Split(strings.TrimSpace(sidecar), "\n")
	if len(lines) < 2 || !strings.HasPrefix(lines[0], "sha256:") || !sha256HexRE.MatchString(strings.TrimPrefix(lines[0], "sha256:")) {
		return "", "", false
	}
	for _, line := range lines[1:] {
		if value, found := strings.CutPrefix(line, "guest-init-sha256="); found && sha256HexRE.MatchString(value) {
			return lines[0], value, true
		}
	}
	return "", "", false
}
