package fcvm

// adr: 431. Seal complete approved source streams before jail provisioning.

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"slices"
	"sync"

	"github.com/gofrs/flock"
	"github.com/onebox-faas/faas/pkg/rootfs"
	"github.com/onebox-faas/faas/pkg/runtimeadmission"
	"github.com/onebox-faas/faas/pkg/storage"
)

type verifiedSourceVMM interface {
	BootColdBootVerified(context.Context, Lease, ColdBootSpec, []runtimeadmission.ArtifactSource) error
}

func (m *Manager) checkAdmittedArtifactSources(req WakeRequest) error {
	if err := CheckWakeArtifactSources(req); err != nil {
		return err
	}
	if len(req.ArtifactSources) != 0 {
		if _, capable := m.vmm.(verifiedSourceVMM); !capable || req.KeepPaused {
			return runtimeadmission.ErrUnavailable
		}
	}
	return nil
}

func CheckWakeArtifactSources(req WakeRequest) error {
	if len(req.ArtifactSources) == 0 {
		return nil
	}
	sidecars := map[string]string{}
	for _, workload := range req.Sidecars {
		if _, duplicate := sidecars[workload.Name]; duplicate {
			return runtimeadmission.ErrInvalid
		}
		sidecars[workload.Name] = workload.StorageKey
	}
	return runtimeadmission.CheckArtifactSources(req.ArtifactSources, req.BaseKey, req.LayerKey, sidecars)
}

func (m *Manager) bootColdBootWithSources(ctx context.Context, lease Lease, spec ColdBootSpec, sources []runtimeadmission.ArtifactSource) error {
	if len(sources) == 0 {
		return m.vmm.BootColdBoot(ctx, lease, spec)
	}
	backend, ok := m.vmm.(verifiedSourceVMM)
	if !ok {
		return runtimeadmission.ErrUnavailable
	}
	return backend.BootColdBootVerified(ctx, lease, spec, sources)
}

type sealedRuntimeSource struct {
	path  string
	bytes int64
	users map[string]bool
	ready chan struct{}
	err   error
}

// Only live native consumers retain these files. Sharing an immutable inode
// preserves the shared read-only base; writable drives still clone separately.
type runtimeSourceCache struct {
	mu      sync.Mutex
	root    string
	parent  string
	lock    *flock.Flock
	owners  map[string]bool
	entries map[string]*sealedRuntimeSource
}

func newRuntimeSourceCache() *runtimeSourceCache {
	return &runtimeSourceCache{entries: map[string]*sealedRuntimeSource{}, owners: map[string]bool{}}
}

func (v *JailerVMM) runtimeSources() *runtimeSourceCache {
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.verifiedRuntimeSources == nil {
		v.verifiedRuntimeSources = newRuntimeSourceCache()
		v.verifiedRuntimeSources.parent = v.runtimeSourceRoot
	}
	return v.verifiedRuntimeSources
}

func (c *runtimeSourceCache) acquire(ctx context.Context, backend storage.StorageBackend, instance string, source runtimeadmission.ArtifactSource) (string, error) {
	if backend == nil || instance == "" || !source.Valid() {
		return "", runtimeadmission.ErrInvalid
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	c.mu.Lock()
	entry, present := c.entries[source.Digest]
	if present && entry.bytes != source.Bytes {
		c.mu.Unlock()
		return "", runtimeadmission.ErrInvalid
	}
	if err := c.retainOwner(instance); err != nil {
		err = errors.Join(err, c.removeEmptyRoot())
		c.mu.Unlock()
		return "", err
	}
	if !present {
		entry = &sealedRuntimeSource{bytes: source.Bytes, users: map[string]bool{}, ready: make(chan struct{})}
		c.entries[source.Digest] = entry
	}
	entry.users[instance] = true
	c.mu.Unlock()
	if !present {
		c.load(ctx, backend, source, entry)
	}
	path, err := c.wait(ctx, instance, source.Digest, entry)
	if err != nil {
		err = errors.Join(err, c.release(instance))
	}
	return path, err
}

func (c *runtimeSourceCache) wait(ctx context.Context, instance, digest string, entry *sealedRuntimeSource) (string, error) {
	select {
	case <-ctx.Done():
		return "", ctx.Err()
	case <-entry.ready:
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := errors.Join(entry.err, ctx.Err()); err != nil {
		return "", err
	}
	if c.entries[digest] != entry || !entry.users[instance] {
		return "", runtimeadmission.ErrStale
	}
	return entry.path, nil
}

func (c *runtimeSourceCache) load(ctx context.Context, backend storage.StorageBackend, source runtimeadmission.ArtifactSource, entry *sealedRuntimeSource) {
	path, err := c.download(ctx, backend, source)
	c.mu.Lock()
	defer c.mu.Unlock()
	entry.path, entry.err = path, err
	close(entry.ready)
	if len(entry.users) == 0 {
		entry.err = errors.Join(entry.err, c.removeEntry(source.Digest, entry), c.removeEmptyRoot())
	}
}

func (c *runtimeSourceCache) ensureRoot() (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.ensureRootLocked(); err != nil {
		return "", err
	}
	return c.root, nil
}

func (c *runtimeSourceCache) download(ctx context.Context, backend storage.StorageBackend, source runtimeadmission.ArtifactSource) (string, error) {
	root, err := c.ensureRoot()
	if err != nil {
		return "", err
	}
	reader, err := backend.Get(ctx, source.StorageKey)
	if err != nil {
		return "", fmt.Errorf("runtime source: storage read: %w", err)
	}
	if reader == nil {
		return "", runtimeadmission.ErrInvalid
	}
	closeReader := closeRuntimeSourceReaderOnCancel(ctx, reader)
	file, err := os.CreateTemp(root, "faas-snap-runtime-")
	if err != nil {
		return "", errors.Join(err, closeReader())
	}
	err = writeSealedRuntimeSource(ctx, file, reader, source)
	err = errors.Join(err, closeReader(), file.Close(), ctx.Err())
	if err == nil {
		err = os.Chmod(file.Name(), 0o444)
	}
	if err != nil {
		removeErr := os.Remove(file.Name())
		if removeErr != nil && !os.IsNotExist(removeErr) {
			return file.Name(), errors.Join(err, removeErr)
		}
		return "", err
	}
	return file.Name(), nil
}

func closeRuntimeSourceReaderOnCancel(ctx context.Context, reader io.ReadCloser) func() error {
	var once sync.Once
	var err error
	closeReader := func() { once.Do(func() { err = reader.Close() }) }
	stop := context.AfterFunc(ctx, closeReader)
	return func() error {
		stop()
		closeReader()
		return err
	}
}

// The bytes hashed are the bytes written. The mutable backend pathname is
// never reopened or linked, including local/cache-backed Get implementations.
func writeSealedRuntimeSource(ctx context.Context, file *os.File, reader io.Reader, source runtimeadmission.ArtifactSource) error {
	if !source.Valid() {
		return runtimeadmission.ErrInvalid
	}
	actual, err := rootfs.ReadArtifactIdentity(ctx, io.TeeReader(io.LimitReader(reader, source.Bytes+1), file))
	if err != nil {
		return fmt.Errorf("runtime source: complete stream: %w", err)
	}
	if actual.Digest != source.Digest || actual.Bytes != source.Bytes {
		return fmt.Errorf("runtime source: complete artifact identity mismatch: %w", runtimeadmission.ErrInvalid)
	}
	return errors.Join(file.Sync(), ctx.Err())
}

func (c *runtimeSourceCache) release(instance string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	var result error
	for digest, entry := range c.entries {
		delete(entry.users, instance)
		if len(entry.users) != 0 {
			continue
		}
		select {
		case <-entry.ready:
			result = errors.Join(result, c.removeEntry(digest, entry))
		default:
			// The downloader joins its own context and sweeps an unclaimed result.
		}
	}
	return errors.Join(result, c.releaseOwner(instance), c.removeEmptyRoot())
}

func (c *runtimeSourceCache) removeEntry(digest string, entry *sealedRuntimeSource) error {
	if entry.path != "" {
		if err := os.Remove(entry.path); err != nil && !os.IsNotExist(err) {
			return err // Keep cleanup ownership for a subsequent retry.
		}
	}
	delete(c.entries, digest)
	return nil
}

func (c *runtimeSourceCache) removeEmptyRoot() error {
	if len(c.entries) != 0 || c.root == "" {
		return nil
	}
	if err := os.RemoveAll(c.root); err != nil {
		return err
	}
	if err := c.lock.Close(); err != nil {
		return err
	}
	c.root = ""
	c.lock = nil
	clear(c.owners)
	return nil
}

func (v *JailerVMM) releaseRuntimeSources(instance string) error {
	observationErr := v.releaseRuntimeDriveHandoff(instance)
	v.mu.Lock()
	cache := v.verifiedRuntimeSources
	v.mu.Unlock()
	if cache == nil {
		return observationErr
	}
	return errors.Join(observationErr, cache.release(instance))
}

func (v *JailerVMM) BootColdBootVerified(ctx context.Context, lease Lease, spec ColdBootSpec, sources []runtimeadmission.ArtifactSource) (err error) {
	spec, err = v.prepareVerifiedColdBoot(ctx, lease, spec, sources)
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			err = errors.Join(err, v.releaseRuntimeSources(lease.Instance))
		}
	}()
	return v.BootColdBoot(ctx, lease, spec)
}

func (v *JailerVMM) prepareVerifiedColdBoot(ctx context.Context, lease Lease, spec ColdBootSpec, sources []runtimeadmission.ArtifactSource) (result ColdBootSpec, err error) {
	mainKey := spec.LayerKey
	sidecars := map[string]string{}
	if len(spec.Workloads) != 0 {
		mainKey = spec.Workloads[0].StorageKey
		for _, workload := range spec.Workloads[1:] {
			if _, duplicate := sidecars[workload.Name]; duplicate {
				return spec, runtimeadmission.ErrInvalid
			}
			sidecars[workload.Name] = workload.StorageKey
		}
	}
	if spec.Validate() != nil || runtimeadmission.CheckArtifactSources(sources, spec.BaseKey, mainKey, sidecars) != nil {
		return spec, runtimeadmission.ErrInvalid
	}
	if err := v.registerRuntimeDriveHandoff(lease, spec, sources); err != nil {
		return spec, err
	}
	defer func() {
		if err != nil {
			err = errors.Join(err, v.releaseRuntimeSources(lease.Instance))
		}
	}()
	paths := map[string]string{}
	for _, source := range sources {
		path, err := v.runtimeSources().acquire(ctx, v.storage, lease.Instance, source)
		if err != nil {
			return spec, err
		}
		paths[source.Role()] = path
	}
	spec.BaseKey = paths["base"]
	spec.Workloads = slices.Clone(spec.Workloads)
	if len(spec.Workloads) == 0 {
		spec.LayerKey = paths["main"]
	} else {
		spec.Workloads[0].StorageKey = paths["main"]
		for i := 1; i < len(spec.Workloads); i++ {
			spec.Workloads[i].StorageKey = paths["sidecar:"+spec.Workloads[i].Name]
		}
	}
	return spec, ctx.Err()
}
