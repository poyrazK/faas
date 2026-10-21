// Package gitarchive builds deterministic provider archives for acceptance.
package gitarchive

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"path"
	"slices"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
)

const ManifestPath = "environments/production.yaml"

// Create freezes both the reviewed definition and customer build context at
// one commit. Never accept paths that escape or replace the manifest.
func Create(commit string, definition api.EnvironmentDefinition, files map[string][]byte) ([]byte, error) {
	decoded, err := hex.DecodeString(commit)
	if err != nil || len(decoded) != 20 || strings.ToLower(commit) != commit {
		return nil, fmt.Errorf("fixture archive: invalid commit")
	}
	body, err := json.Marshal(definition)
	if err != nil {
		return nil, err
	}
	contents := map[string][]byte{ManifestPath: body}
	for name, data := range files {
		if name == ManifestPath || name == "." || name == ".." || path.Clean(name) != name || strings.HasPrefix(name, "/") || strings.HasPrefix(name, "../") || strings.ContainsAny(name, "\\\x00") {
			return nil, fmt.Errorf("fixture archive: invalid build path %q", name)
		}
		contents[name] = data
	}
	names := make([]string, 0, len(contents))
	for name := range contents {
		names = append(names, name)
	}
	slices.Sort(names)
	var archive bytes.Buffer
	gz := gzip.NewWriter(&archive)
	tw := tar.NewWriter(gz)
	for _, name := range names {
		data := contents[name]
		if err := tw.WriteHeader(&tar.Header{Name: "shop-" + commit + "/" + name, Mode: 0o644, Size: int64(len(data)), Typeflag: tar.TypeReg}); err != nil {
			return nil, err
		}
		if _, err := tw.Write(data); err != nil {
			return nil, err
		}
	}
	if err := tw.Close(); err != nil {
		return nil, err
	}
	if err := gz.Close(); err != nil {
		return nil, err
	}
	return archive.Bytes(), nil
}
