package templates

import (
	"archive/tar"
	"bufio"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"path"
	"slices"
	"strings"
	"sync"
)

// DataAPIRuntimeVersionFile names the runtime version inside the data-api
// template, the archive apid serves and the running image (ADR-20261031095954).
const DataAPIRuntimeVersionFile = "runtime-version.json"

// DataAPIRuntimeArchiveRoot is the directory every runtime archive entry
// lives under; the builder resolves it like any single-directory upload.
const DataAPIRuntimeArchiveRoot = "data-api"

// DataAPIRuntimeFiles returns the runtime's deployable files keyed by path
// below the runtime root. Tests and dotfiles stay in the repository.
func DataAPIRuntimeFiles() (map[string][]byte, error) {
	root, err := fs.Sub(FS, "data-api")
	if err != nil {
		return nil, err
	}
	files := map[string][]byte{}
	err = fs.WalkDir(root, ".", func(p string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if p == "." {
			return nil
		}
		if strings.HasPrefix(path.Base(p), ".") || p == "test" {
			if d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if d.IsDir() {
			return nil
		}
		content, readErr := fs.ReadFile(root, p)
		if readErr != nil {
			return readErr
		}
		files[p] = content
		return nil
	})
	return files, err
}

var dataAPIRuntimeArchive = sync.OnceValues(func() ([]byte, error) {
	files, err := DataAPIRuntimeFiles()
	if err != nil {
		return nil, err
	}
	return DataAPIRuntimeArchiveOf(files)
})

// DataAPIRuntimeArchive returns the gzipped tar of DataAPIRuntimeFiles that
// apid serves. It is deterministic, so every apid built from one commit
// serves the same bytes.
func DataAPIRuntimeArchive() ([]byte, error) { return dataAPIRuntimeArchive() }

// DataAPIRuntimeArchiveOf writes files as a deterministic gzipped tar under
// DataAPIRuntimeArchiveRoot.
func DataAPIRuntimeArchiveOf(files map[string][]byte) ([]byte, error) {
	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	slices.Sort(names)
	var out bytes.Buffer
	gz := gzip.NewWriter(&out)
	tw := tar.NewWriter(gz)
	for _, name := range names {
		content := files[name]
		if err := tw.WriteHeader(&tar.Header{Name: DataAPIRuntimeArchiveRoot + "/" + name, Mode: 0o644, Size: int64(len(content)), Typeflag: tar.TypeReg}); err != nil {
			return nil, err
		}
		if _, err := tw.Write(content); err != nil {
			return nil, err
		}
	}
	if err := tw.Close(); err != nil {
		return nil, err
	}
	if err := gz.Close(); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

// DataAPIRuntimeVersion returns the Data API runtime version this binary
// carries. apid serves this runtime; a running Data API reports the version it
// was built from, so a different one is not the platform's current runtime.
func DataAPIRuntimeVersion() (string, error) {
	raw, err := fs.ReadFile(FS, "data-api/"+DataAPIRuntimeVersionFile)
	if err != nil {
		return "", err
	}
	var file struct {
		Version string `json:"version"`
	}
	if err = json.Unmarshal(raw, &file); err != nil {
		return "", fmt.Errorf("parse %s: %w", DataAPIRuntimeVersionFile, err)
	}
	if !ValidDataAPIRuntimeVersion(file.Version) {
		return "", fmt.Errorf("%s has no valid version", DataAPIRuntimeVersionFile)
	}
	return file.Version, nil
}

// ValidDataAPIRuntimeVersion reports whether v has the shape of a runtime
// version: 16 lowercase hex characters.
func ValidDataAPIRuntimeVersion(v string) bool {
	if len(v) != 16 {
		return false
	}
	_, err := hex.DecodeString(v)
	return err == nil && strings.ToLower(v) == v
}

// DataAPIRuntimeDigest hashes the files that make up the runtime image: the
// Dockerfile, the Procfile and every file the Dockerfile copies from the
// source. Docs and tests are left out, so editing them does not mark every
// running Data API as out of date. The version file must equal this digest.
func DataAPIRuntimeDigest() (string, error) {
	root, err := fs.Sub(FS, "data-api")
	if err != nil {
		return "", err
	}
	dockerfile, err := fs.ReadFile(root, "Dockerfile")
	if err != nil {
		return "", err
	}
	names := []string{"Dockerfile", "Procfile"}
	copied, err := dockerfileSourceCopies(dockerfile)
	if err != nil {
		return "", err
	}
	for _, name := range copied {
		if name != DataAPIRuntimeVersionFile && !slices.Contains(names, name) {
			names = append(names, name)
		}
	}
	slices.Sort(names)
	h := sha256.New()
	for _, name := range names {
		content, err := fs.ReadFile(root, name)
		if err != nil {
			return "", fmt.Errorf("runtime file %s: %w", name, err)
		}
		var size [8]byte
		binary.BigEndian.PutUint64(size[:], uint64(len(content)))
		h.Write([]byte(name))
		h.Write([]byte{0})
		h.Write(size[:])
		h.Write(content)
	}
	return hex.EncodeToString(h.Sum(nil))[:16], nil
}

// dockerfileSourceCopies lists the build-context files named by COPY
// instructions; COPY --from copies from another stage and is skipped.
func dockerfileSourceCopies(dockerfile []byte) ([]string, error) {
	var out []string
	scanner := bufio.NewScanner(bytes.NewReader(dockerfile))
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) < 3 || !strings.EqualFold(fields[0], "COPY") || strings.HasPrefix(fields[1], "--") {
			continue
		}
		for _, source := range fields[1 : len(fields)-1] {
			if strings.ContainsAny(source, "*?[") || source == "." {
				return nil, fmt.Errorf("data-api Dockerfile copies %q; name runtime files explicitly", source)
			}
			out = append(out, source)
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	if len(out) == 0 {
		return nil, errors.New("data-api Dockerfile copies no runtime files")
	}
	return out, nil
}
