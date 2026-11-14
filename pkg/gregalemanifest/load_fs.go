package gregalemanifest

import (
	"errors"
	"fmt"
	"io/fs"
	"path"
)

// LoadFS is Load over a source tree: it reads gregale.yaml, gregale.yml or
// gregale.toml in dir (slash-separated, relative to the tree's root).
func LoadFS(fsys fs.FS, dir string) (*Manifest, bool, error) {
	for _, name := range []string{"gregale.yaml", "gregale.yml", "gregale.toml"} {
		p := path.Join(dir, name)
		b, err := fs.ReadFile(fsys, p)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, false, fmt.Errorf("gregalemanifest: read %s: %w", p, err)
		}
		var m *Manifest
		if name == "gregale.toml" {
			m, err = parseTOMLManifest(b)
		} else {
			m, err = parseManifest(b)
		}
		if err != nil {
			return nil, false, fmt.Errorf("gregalemanifest: parse %s: %w", p, err)
		}
		return m, true, nil
	}
	return nil, false, nil
}
