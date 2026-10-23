package builderd

import (
	"fmt"
	"path/filepath"

	"github.com/onebox-faas/faas/pkg/sourcecontext"
	"github.com/onebox-faas/faas/pkg/tarball"
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

// vmSourceRoot is the archive-relative root the builder VM works in. guest-init
// extracts the archive as delivered, so a transport wrapper (the CLI's single
// directory, or a GitOps canonical context's gregale-source/) stays in place
// below a non-default root; framework detection already resolves it the same
// way. A default root is unchanged: guest-init flattens the wrapper itself.
// The persisted repository-relative root still keys caches and reuse.
func vmSourceRoot(archive, sourceRoot string) (string, error) {
	root, err := sourcecontext.EffectiveRoot(sourceRoot)
	if err != nil || root == sourcecontext.DefaultRoot {
		return sourceRoot, err
	}
	return tarball.ResolveSourceRoot(archive, root)
}

func buildDockerfilePath(raw string) (string, error) {
	if raw == "" {
		return "", nil
	}
	normalized, err := sourcecontext.Normalize(raw)
	if err != nil {
		return "", err
	}
	if normalized == sourcecontext.DefaultRoot {
		return "", fmt.Errorf("dockerfile path must name a file")
	}
	return normalized, nil
}
