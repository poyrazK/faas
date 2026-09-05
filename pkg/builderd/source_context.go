package builderd

import (
	"path/filepath"

	"github.com/onebox-faas/faas/pkg/sourcecontext"
)

// buildWorkdir maps the persisted repository-relative source root to the
// guest path used by builderd and guest-init. Keeping the validation here as
// well as at the API boundary makes retries and older/internal callers fail
// closed before a VM is started.
func buildWorkdir(sourceRoot string) (string, error) {
	root, err := sourcecontext.EffectiveRoot(sourceRoot)
	if err != nil {
		return "", err
	}
	if root == sourcecontext.DefaultRoot {
		return "/build/src", nil
	}
	return filepath.Join("/build/src", filepath.FromSlash(root)), nil
}
