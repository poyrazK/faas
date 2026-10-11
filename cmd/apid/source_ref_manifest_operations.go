package main

import (
	"archive/tar"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"path"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/gregalemanifest"
)

func resolveSourceOperations(sourcePath, manifestPath, slug string, plan api.Plan, m *gregalemanifest.Manifest) error {
	// ResolveOperations validates the declaration count before invoking this
	// reader. One archive pass bounds decompression independently of the number
	// of schemas, and only the selected app's bounded files are retained.
	var schemas map[string][]byte
	return m.ResolveOperations(slug, plan, func(file string, limit int) ([]byte, error) {
		if schemas == nil {
			names := map[string]bool{}
			for _, operation := range m.Operations {
				if operation.App == "" || operation.App == slug {
					names[path.Join(path.Dir(manifestPath), operation.InputSchema)] = true
					names[path.Join(path.Dir(manifestPath), operation.OutputSchema)] = true
				}
			}
			var err error
			schemas, err = readOperationSourceSchemas(sourcePath, names, limit)
			if err != nil {
				return nil, err
			}
		}
		return schemas[path.Join(path.Dir(manifestPath), file)], nil
	})
}

func readOperationSourceSchemas(sourcePath string, names map[string]bool, limit int) (map[string][]byte, error) {
	f, err := openSpoolFile(sourcePath)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	z, err := gzip.NewReader(f)
	if err != nil {
		return nil, err
	}
	defer func() { _ = z.Close() }()
	reader := tar.NewReader(z)
	found := map[string][]byte{}
	for {
		header, err := reader.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, err
		}
		name := strings.TrimPrefix(header.Name, "./")
		if !names[name] {
			continue
		}
		if _, duplicate := found[name]; duplicate || header.Typeflag != tar.TypeReg || header.Size > int64(limit) {
			return nil, fmt.Errorf("schema must be one regular file of at most %d bytes", limit)
		}
		body, err := io.ReadAll(io.LimitReader(reader, int64(limit)+1))
		if err != nil {
			return nil, fmt.Errorf("read bounded schema: %w", err)
		}
		if len(body) > limit {
			return nil, fmt.Errorf("schema exceeds %d bytes", limit)
		}
		found[name] = body
	}
	for name := range names {
		if _, present := found[name]; !present {
			return nil, fmt.Errorf("schema file %q is missing", name)
		}
	}
	return found, nil
}

func sourceOperationSpecs(m *gregalemanifest.Manifest) []api.OperationDefinitionSpec {
	if m == nil {
		return nil
	}
	return m.ResolvedOperations
}
