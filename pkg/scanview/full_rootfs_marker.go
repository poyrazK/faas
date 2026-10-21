package scanview

import (
	"bytes"
	"context"
	"errors"
	"io/fs"
	"path"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
)

// FullRootfsMarkerPresent uses guest-root symlink semantics before pivot.
// Guest selection and native scanner selection must interpret the same image;
// an absolute parent symlink never redirects the marker lookup into the host.
func FullRootfsMarkerPresent(ctx context.Context, dir string) (present bool, retErr error) {
	root, err := openDirectory(dir)
	if err != nil {
		return false, err
	}
	defer func() {
		retErr = errors.Join(retErr, root.Close())
		if retErr != nil {
			present = false
		}
	}()
	parent, err := virtualLink(ctx, root, ".marker", path.Dir(api.FullRootfsMarkerPath))
	if err != nil {
		return false, err
	}
	if parent == brokenLink {
		return false, nil
	}
	name := path.Join(strings.TrimPrefix(parent, "/"), path.Base(api.FullRootfsMarkerPath))
	info, err := root.Lstat(name)
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	if err != nil || !info.Mode().IsRegular() || info.Size() != int64(len(api.FullRootfsMarkerValue)) {
		return false, errors.Join(ErrInvalid, err)
	}
	var body bytes.Buffer
	if _, err := readRegular(ctx, root, name, info, &body); err != nil {
		return false, err
	}
	if body.String() != api.FullRootfsMarkerValue {
		return false, ErrInvalid
	}
	return true, nil
}
