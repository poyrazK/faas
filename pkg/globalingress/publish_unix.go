//go:build linux || darwin

package globalingress

// adr: 786
import (
	"errors"
	"os"
	"path/filepath"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"golang.org/x/sys/unix"
)

// Open the configured directory without traversing symlinks. The directory
// must belong to the publisher UID and forbid group/other mutation. A shared
// read-only group may read the 0640 projection; only APID may replace it.
func publishDirectory(path string) (*os.File, error) {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path || filepath.Base(path) == "." || filepath.Base(path) == "/" {
		return nil, ErrUnavailable
	}
	fd, err := unix.Open(filepath.Dir(path), unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	if err != nil {
		return nil, errors.Join(err, ErrUnavailable)
	}
	f := os.NewFile(uintptr(fd), filepath.Dir(path))
	var st unix.Stat_t
	if err := unix.Fstat(fd, &st); err != nil || st.Uid != uint32(os.Geteuid()) || st.Mode&0o022 != 0 {
		_ = f.Close()
		return nil, ErrUnavailable
	}
	// EvalSymlinks rejects symlinked ancestors as well as the final directory.
	actual, err := filepath.EvalSymlinks(filepath.Dir(path))
	if err != nil || actual != filepath.Dir(path) {
		_ = f.Close()
		return nil, ErrUnavailable
	}
	return f, nil
}
func CheckPublishDirectory(path string) error {
	f, err := publishDirectory(path)
	if err == nil {
		err = f.Close()
	}
	return err
}

// WriteManifest publishes only complete bytes: exclusive same-directory temp,
// fsync, atomic rename, then directory fsync. The durable producer serializes
// calls; no generation is assigned here and no signature is altered.
func WriteManifest(path string, raw []byte) error {
	if len(raw) == 0 || len(raw) > api.GlobalIngressEnvelopeMaxBytes {
		return ErrUnavailable
	}
	dir, err := publishDirectory(path)
	if err != nil {
		return err
	}
	defer func() { _ = dir.Close() }()
	fd := int(dir.Fd())
	base := filepath.Base(path)
	var st unix.Stat_t
	err = unix.Fstatat(fd, base, &st, unix.AT_SYMLINK_NOFOLLOW)
	if err == nil && (uint32(st.Mode)&unix.S_IFMT != unix.S_IFREG || st.Nlink != 1 || st.Uid != uint32(os.Geteuid()) || st.Mode&0o022 != 0) {
		return ErrUnavailable
	}
	if err != nil && !errors.Is(err, unix.ENOENT) {
		return errors.Join(err, ErrUnavailable)
	}
	temp := ".gregale-ingress-" + uuid.NewString()
	tmp, err := unix.Openat(fd, temp, unix.O_WRONLY|unix.O_CREAT|unix.O_EXCL|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0o640)
	if err != nil {
		return err
	}
	f := os.NewFile(uintptr(tmp), temp)
	defer func() { _ = f.Close(); _ = unix.Unlinkat(fd, temp, 0) }()
	if _, err = f.Write(raw); err != nil {
		return err
	}
	if err = f.Chmod(0o640); err != nil {
		return err
	}
	if err = f.Sync(); err != nil {
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	if err = unix.Renameat(fd, temp, fd, base); err != nil {
		return err
	}
	return dir.Sync()
}
