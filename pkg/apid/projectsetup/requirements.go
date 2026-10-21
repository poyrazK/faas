package projectsetup

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"path"
	"slices"

	"github.com/onebox-faas/faas/pkg/api"
)

// RequiredNames reads the CLI's explicit array of required secret names.
// Workload-local declarations override the repository declaration. Detected
// environment keys do not establish requirements when this file is absent.
func RequiredNames(fsys fs.FS, root string) ([]string, error) {
	paths := []string{path.Join(root, ".gregale/required-secrets.json")}
	if root != "" && root != "." {
		paths = append(paths, ".gregale/required-secrets.json")
	}
	for _, name := range paths {
		file, err := fsys.Open(name)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("read required-secret declaration: %w", err)
		}
		body, err := io.ReadAll(io.LimitReader(file, 65537))
		_ = file.Close()
		var names []string
		if err != nil || len(body) > 65536 || json.Unmarshal(body, &names) != nil || names == nil {
			return nil, fmt.Errorf("required-secret declaration must be a JSON array of names within 64 KiB")
		}
		for _, key := range names {
			if api.ValidateSecretKey(key) != nil {
				return nil, fmt.Errorf("required-secret declaration contains an invalid key name")
			}
		}
		slices.Sort(names)
		return slices.Compact(names), nil
	}
	return nil, nil
}
