package main

import (
	"archive/tar"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"path"
	"sort"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/gregalemanifest"
	"github.com/onebox-faas/faas/pkg/tarball"
)

func cliRevisionPinTTLSeconds(ttl time.Duration) (int, error) {
	if ttl < 0 || ttl%time.Second != 0 || ttl/time.Second > api.RevisionPinMaxTTLSeconds {
		return 0, fmt.Errorf("must be whole seconds between 0 and %ds", api.RevisionPinMaxTTLSeconds)
	}
	return int(ttl / time.Second), nil
}

// Read the selected archive, never the possibly dirty checkout. The scan is
// bounded and never extracts customer paths onto the filesystem.
func deploymentQueueCanaryBindings(archive, root string, names []string) ([]string, error) {
	if len(names) == 0 {
		return nil, nil
	}
	selected := make(map[string]bool, len(names))
	for _, name := range names {
		if !api.ValidQueueBindingName(name) {
			return nil, fmt.Errorf("invalid binding name %q", name)
		}
		if selected[name] {
			return nil, fmt.Errorf("duplicate binding %q", name)
		}
		selected[name] = true
	}
	if archive == "" {
		return nil, fmt.Errorf("a source archive containing gregale.yaml is required")
	}
	file, err := openCustomerFile(archive)
	if err != nil {
		return nil, err
	}
	defer func() { _ = file.Close() }()
	gz, err := gzip.NewReader(file)
	if err != nil {
		return nil, err
	}
	defer func() { _ = gz.Close() }()
	bounded := &io.LimitedReader{R: gz, N: deployArchiveMaxExpandedBytes + 1}
	tr := tar.NewReader(bounded)
	manifests := map[string][]byte{}
	for entries := 0; ; entries++ {
		h, readErr := tr.Next()
		if errors.Is(readErr, io.EOF) {
			break
		}
		if readErr != nil {
			return nil, readErr
		}
		if entries >= api.SourceArchiveMaxEntries {
			return nil, fmt.Errorf("too many archive entries")
		}
		name := path.Clean(h.Name)
		if path.Base(name) != "gregale.yaml" && path.Base(name) != "gregale.yml" {
			continue
		}
		if _, exists := manifests[name]; exists {
			return nil, fmt.Errorf("ambiguous manifests in source archive")
		}
		if h.Typeflag != tar.TypeReg || h.Size > api.SourceManifestMaxBytes {
			return nil, fmt.Errorf("manifest must be a bounded regular file")
		}
		contents, readErr := io.ReadAll(tr)
		if readErr != nil {
			return nil, readErr
		}
		manifests[name] = contents
	}
	if bounded.N <= 0 {
		return nil, fmt.Errorf("source archive exceeds expanded scan limit")
	}
	resolvedRoot, err := tarball.ResolveSourceRoot(archive, root)
	if err != nil {
		return nil, err
	}
	var manifest *gregalemanifest.Manifest
	for _, name := range []string{"gregale.yaml", "gregale.yml"} {
		if contents, exists := manifests[path.Join(resolvedRoot, name)]; exists {
			if manifest != nil {
				return nil, fmt.Errorf("ambiguous manifests in selected source")
			}
			manifest, err = gregalemanifest.ParseBytes(contents)
			if err != nil {
				return nil, err
			}
		}
	}
	if manifest == nil {
		return nil, fmt.Errorf("selected source must contain gregale.yaml or gregale.yml")
	}
	allowed := map[string]bool{}
	for _, binding := range manifest.QueueBindings {
		allowed[binding.Name] = binding.Mode == "push" && binding.WorkloadClass == "http" && (binding.Enabled == nil || *binding.Enabled)
	}
	result := make([]string, 0, len(selected))
	for name := range selected {
		if !allowed[name] {
			return nil, fmt.Errorf("%q must name an enabled push HTTP queue binding in the deployed manifest", name)
		}
		result = append(result, name)
	}
	sort.Strings(result)
	return result, nil
}
