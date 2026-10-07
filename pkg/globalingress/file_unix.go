//go:build linux || darwin

package globalingress

// adr: 656
import (
	"errors"
	"io"
	"os"
	"path/filepath"

	"github.com/onebox-faas/faas/pkg/api"
	"golang.org/x/sys/unix"
)

// ReadManifest reads the publisher's atomically replaced regular file without
// following symlinks or blocking on a substituted FIFO. Signatures own authority.
func ReadManifest(path string) ([]byte, error) {
	if !filepath.IsAbs(path) {
		return nil, ErrUnavailable
	}
	fd, err := unix.Open(path, unix.O_RDONLY|unix.O_CLOEXEC|unix.O_NOFOLLOW|unix.O_NONBLOCK, 0)
	if err != nil {
		return nil, errors.Join(err, ErrUnavailable)
	}
	f := os.NewFile(uintptr(fd), path)
	defer func() { _ = f.Close() }()
	var st unix.Stat_t
	if err = unix.Fstat(fd, &st); err != nil {
		return nil, errors.Join(err, ErrUnavailable)
	}
	if uint32(st.Mode)&unix.S_IFMT != unix.S_IFREG || st.Nlink != 1 || st.Mode&0o022 != 0 || st.Size <= 0 || st.Size > api.GlobalIngressEnvelopeMaxBytes {
		return nil, ErrUnavailable
	}
	raw, err := io.ReadAll(io.LimitReader(f, api.GlobalIngressEnvelopeMaxBytes+1))
	if err != nil || len(raw) > api.GlobalIngressEnvelopeMaxBytes {
		return nil, errors.Join(err, ErrUnavailable)
	}
	return raw, nil
}
