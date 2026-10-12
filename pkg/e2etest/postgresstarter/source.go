// Package postgresstarter uploads the shipped customer starter unchanged. The
// only fixture file is a public CA for the disposable SQL server.
package postgresstarter

import (
	"bytes"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"io/fs"

	"github.com/onebox-faas/faas/cmd/gregale/templates"
)

func Source(ca []byte) (map[string]string, error) {
	rest := bytes.TrimSpace(ca)
	certificates := 0
	for len(rest) > 0 {
		if !bytes.HasPrefix(rest, []byte("-----BEGIN CERTIFICATE-----")) {
			return nil, fmt.Errorf("starter fixture trust must contain certificates only")
		}
		block, remaining := pem.Decode(rest)
		if block == nil || block.Type != "CERTIFICATE" || len(block.Headers) != 0 {
			return nil, fmt.Errorf("starter fixture trust must contain certificates only")
		}
		if _, err := x509.ParseCertificate(block.Bytes); err != nil {
			return nil, fmt.Errorf("starter fixture certificate: %w", err)
		}
		certificates++
		rest = bytes.TrimSpace(remaining)
	}
	if certificates == 0 {
		return nil, fmt.Errorf("starter fixture CA is required")
	}
	source := make(map[string]string)
	err := fs.WalkDir(templates.FS, "rest-api-postgres", func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		content, err := fs.ReadFile(templates.FS, path)
		if err != nil {
			return err
		}
		source[path[len("rest-api-postgres/"):]] = string(content)
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("read embedded PostgreSQL starter: %w", err)
	}
	source["fixture-ca.pem"] = string(ca)
	return source, nil
}
