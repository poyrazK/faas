package fcvm

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"syscall"
)

// A builder's writable drives are handed to its jail uid while it runs
// (stageEphemeralWritableAsForOwner chmods the source 0600 and chowns it).
// builderd only removes its scratch drive afterwards, but it keeps the drive
// a BuildKit seed run filled and reflinks a copy for every later build
// (ADR-20261030122204), so it must be able to read it again. Production ran
// every build without the seed because the seed stayed owned by the seed
// run's jail uid ("permission denied", 2026-11-17).

// noteBuildKitSeedSource records the host drive staged as instance's
// BuildKit seed drive.
func (v *JailerVMM) noteBuildKitSeedSource(instance, source string) {
	if instance == "" || source == "" {
		return
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.buildkitSeedSources == nil {
		v.buildkitSeedSources = make(map[string]string)
	}
	v.buildkitSeedSources[instance] = source
}

// handBackBuildKitSeed returns instance's BuildKit seed drive to the owner of
// the directory builderd keeps it in, once the VM has exited and its binds
// are gone. A failure only costs builderd the seed, which it then discards
// and makes again, so it is logged rather than retaining the VM's resources.
func (v *JailerVMM) handBackBuildKitSeed(instance string) {
	v.mu.Lock()
	source, ok := v.buildkitSeedSources[instance]
	delete(v.buildkitSeedSources, instance)
	v.mu.Unlock()
	if !ok {
		return
	}
	if err := handBackToDirectoryOwner(source); err != nil && !os.IsNotExist(err) {
		slog.Default().Warn("vmm: hand back BuildKit seed drive", "instance", instance, "err", err)
	}
}

// handBackToDirectoryOwner gives path, which must be a regular file and is
// never followed through a symlink, to the owner and group of its directory.
func handBackToDirectoryOwner(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("%s is not a regular file", path)
	}
	parent, err := os.Stat(filepath.Dir(path))
	if err != nil {
		return err
	}
	stat, ok := parent.Sys().(*syscall.Stat_t)
	if !ok {
		return fmt.Errorf("%s has unsupported stat metadata", filepath.Dir(path))
	}
	return os.Lchown(path, int(stat.Uid), int(stat.Gid))
}
