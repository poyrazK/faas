package runrelease

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"strings"
	"unicode/utf8"

	"github.com/onebox-faas/faas/pkg/api"
)

func Read(path string) ([]byte, error) {
	f, err := openInput(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	body, err := io.ReadAll(io.LimitReader(f, api.RunReleaseManifestMaxBytes+1))
	if err != nil {
		return nil, err
	}
	if len(body) > api.RunReleaseManifestMaxBytes {
		return nil, errors.New("release input exceeds bound")
	}
	return body, nil
}

func ReadPinned(ctx context.Context, pin File) ([]byte, error) {
	return verifyFile(ctx, pin, false)
}

// Decode rejects unknown, duplicate (including case aliases), trailing, deeply
// nested and non-UTF8 JSON. DecodeObject permits extension fields in the existing
// acceptance summary, while retaining the same structural checks.
func Decode(body []byte, dst any) error       { return decode(body, dst, true) }
func DecodeObject(body []byte, dst any) error { return decode(body, dst, false) }
func decode(body []byte, dst any, strict bool) error {
	if len(body) > api.RunReleaseManifestMaxBytes || !utf8.Valid(body) {
		return errors.New("invalid or oversized release JSON")
	}
	d := json.NewDecoder(bytes.NewReader(body))
	if strict {
		d.DisallowUnknownFields()
	}
	if err := d.Decode(dst); err != nil {
		return err
	}
	if err := d.Decode(new(any)); !errors.Is(err, io.EOF) {
		return errors.New("release input must contain exactly one JSON object")
	}
	return uniqueJSON(json.NewDecoder(bytes.NewReader(body)), 0)
}
func uniqueJSON(d *json.Decoder, depth int) error {
	if depth > api.RunReleaseJSONMaxDepth {
		return errors.New("release JSON nesting exceeds bound")
	}
	token, err := d.Token()
	if err != nil {
		return err
	}
	delim, ok := token.(json.Delim)
	if !ok {
		return nil
	}
	seen := map[string]bool{}
	for d.More() {
		if delim == '{' {
			key, err := d.Token()
			if err != nil {
				return err
			}
			name, ok := key.(string)
			name = strings.ToLower(name)
			if !ok || seen[name] {
				return errors.New("duplicate release JSON key")
			}
			seen[name] = true
		}
		if err := uniqueJSON(d, depth+1); err != nil {
			return err
		}
	}
	_, err = d.Token()
	return err
}

func verifyArtifact(ctx context.Context, pin File, executable bool) error {
	f, err := openInput(pin.Path)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	info, err := f.Stat()
	if err != nil {
		return err
	}
	if executable && info.Mode().Perm()&0o111 == 0 {
		return errors.New("release executable lacks executable mode")
	}
	digest, err := digestFile(ctx, f)
	if err != nil {
		return err
	}
	return require(digest == pin.SHA256, "artifact SHA-256 differs from reviewed pin")
}

func inventory(ctx context.Context, path string, allowAbsent bool) (int, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	if !cleanPath(path) {
		return 0, errors.New("inventory requires a canonical absolute non-root path")
	}
	info, err := os.Lstat(path)
	if allowAbsent && errors.Is(err, os.ErrNotExist) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	if !info.IsDir() || info.Mode().Perm()&0o022 != 0 {
		return 0, errors.New("inventory root must be a directory without group/world write")
	}
	root, err := os.OpenRoot(path)
	if err != nil {
		return 0, err
	}
	defer func() { _ = root.Close() }()
	opened, err := root.Stat(".")
	if err != nil || !os.SameFile(info, opened) {
		return 0, errors.New("inventory root changed while opening")
	}
	count := 0
	err = fs.WalkDir(root.FS(), ".", func(name string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if name == "." {
			return nil
		}
		count++
		if count > api.RunReleaseInventoryMaxEntries {
			return errors.New("release inventory exceeds entry bound")
		}
		if !entry.IsDir() && !entry.Type().IsRegular() {
			return fmt.Errorf("inventory contains a symlink or special file")
		}
		return nil
	})
	return count, err
}
