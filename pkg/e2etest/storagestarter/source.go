// Package storagestarter packages the shipped storage application unchanged.
package storagestarter

import (
	"bytes"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"io/fs"
	"strings"

	"github.com/onebox-faas/faas/cmd/gregale/templates"
)

const CAPath = "runtime/fixture-ca.pem"

// Source adds only public fixture trust. The shipped Dockerfile already copies
// runtime/, so neither the application nor its build/lifecycle files need edits.
// The starter's TrustEnv selects this file through the ordinary application API.
func Source(template string, ca []byte) (map[string]string, error) {
	if _, ok := StarterForTemplate(template); !ok {
		return nil, errors.New("unsupported storage starter")
	}
	rest, count := bytes.TrimSpace(ca), 0
	for len(rest) > 0 {
		if !bytes.HasPrefix(rest, []byte("-----BEGIN CERTIFICATE-----")) {
			return nil, errors.New("storage fixture trust must contain certificates only")
		}
		block, remaining := pem.Decode(rest)
		if block == nil || block.Type != "CERTIFICATE" || len(block.Headers) != 0 {
			return nil, errors.New("storage fixture trust must contain certificates only")
		}
		if _, err := x509.ParseCertificate(block.Bytes); err != nil {
			return nil, errors.New("storage fixture trust certificate invalid")
		}
		count++
		rest = bytes.TrimSpace(remaining)
	}
	if count == 0 {
		return nil, errors.New("storage fixture trust is required")
	}
	files := make(map[string]string)
	err := fs.WalkDir(templates.FS, template, func(path string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return err
		}
		content, err := fs.ReadFile(templates.FS, path)
		if err == nil {
			files[strings.TrimPrefix(path, template+"/")] = string(content)
		}
		return err
	})
	if err != nil {
		return nil, err
	}
	files[CAPath] = string(ca)
	return files, nil
}
