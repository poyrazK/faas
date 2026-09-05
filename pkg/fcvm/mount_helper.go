package fcvm

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// Prefer the helper shipped alongside this exact vmmd release. Older bundles
// retain vmmd's command modes; never search PATH or a different release tree.
func resolveMountHelper(executable string) (string, error) {
	helper := filepath.Join(filepath.Dir(executable), "vmmd-jail-helper")
	info, err := os.Stat(helper)
	if errors.Is(err, os.ErrNotExist) {
		return executable, nil
	}
	if err != nil {
		return "", fmt.Errorf("vmm: stat jail helper: %w", err)
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0o111 == 0 {
		return "", fmt.Errorf("vmm: jail helper %s must be an executable regular file", helper)
	}
	return helper, nil
}
