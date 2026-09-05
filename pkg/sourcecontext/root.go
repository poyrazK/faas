// Package sourcecontext contains the validation and normalization rules for
// a build source root inside an uploaded source archive.
package sourcecontext

import (
	"fmt"
	"io/fs"
	"strings"
)

// DefaultRoot is the archive root. An empty value is accepted on the wire for
// backwards compatibility and has the same meaning as this value.
const DefaultRoot = "."

// MaxRootBytes bounds the multipart source_root field. Repository-relative
// paths are short in practice; the bound also prevents an untrusted form field
// from becoming an unbounded allocation before validation.
const MaxRootBytes = 256

// Normalize validates a repository-relative source root and returns its
// canonical slash-separated form. The root is deliberately stricter than
// filepath.Clean: accepting a path that needs cleaning would make the value
// mean different things to tar, filepath, and BuildKit.
func Normalize(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" || raw == DefaultRoot {
		return DefaultRoot, nil
	}
	if len(raw) > MaxRootBytes {
		return "", fmt.Errorf("source root is longer than %d bytes", MaxRootBytes)
	}
	if strings.ContainsRune(raw, 0) {
		return "", fmt.Errorf("source root contains a NUL byte")
	}
	if strings.Contains(raw, `\`) {
		return "", fmt.Errorf("source root must use '/' separators")
	}
	if !fs.ValidPath(raw) || strings.HasPrefix(raw, "/") {
		return "", fmt.Errorf("source root must be a relative path without '.' or '..' components")
	}
	return raw, nil
}

// StorageRoot converts a normalized root into the nullable/empty storage
// representation used by state.Deployment. Empty means archive root, which
// keeps existing deployment responses and rows wire-compatible.
func StorageRoot(raw string) (string, error) {
	root, err := Normalize(raw)
	if err != nil {
		return "", err
	}
	if root == DefaultRoot {
		return "", nil
	}
	return root, nil
}

// EffectiveRoot converts the stored representation back into a usable root.
func EffectiveRoot(stored string) (string, error) {
	return Normalize(stored)
}
