// Package project checks durable-entity developer artifacts without executing application code.
package project

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha512"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/durableentity/validatorbundle"
)

const Filename = "gregale.entities.json"

type ClientContract struct {
	Module string `json:"module"`
	Export string `json:"export"`
}
type Handler struct {
	Client        *ClientContract `json:"client,omitempty"`
	Namespace     string          `json:"namespace"`
	SchemaVersion uint32          `json:"schema_version"`
	Module        string          `json:"module"`
	Export        string          `json:"export"`
}
type Config struct {
	Version          int       `json:"version"`
	Entrypoint       string    `json:"entrypoint"`
	Handlers         []Handler `json:"handlers"`
	RestoreValidator string    `json:"restore_validator,omitempty"`
}

var exportName = regexp.MustCompile(`^[A-Za-z_$][A-Za-z0-9_$]*$`)

// Check returns present=false for ordinary projects. A declared project must be
// ready before upload; this is source validation, not fleet admission or runtime qualification.
func Check(root string) (present bool, err error) {
	if _, err := os.Lstat(filepath.Join(root, Filename)); errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	body, err := readFile(root, Filename, 64<<10)
	if err != nil {
		return true, errors.New("read a regular bounded gregale.entities.json")
	}
	var config Config
	if strict(body, &config) != nil || config.Version != 1 || len(config.Handlers) == 0 || len(config.Handlers) > 64 {
		return true, errors.New("use project version 1 with one to 64 explicit handlers")
	}
	if _, err := readModule(root, config.Entrypoint); err != nil {
		return true, errors.New("entity server entrypoint must be an existing regular JavaScript module")
	}
	seen := map[string]bool{}
	needsClient := false
	for _, handler := range config.Handlers {
		if strings.TrimSpace(handler.Namespace) == "" || len(handler.Namespace) > api.MaxDurableEntityIdentityBytes || (!utf8.ValidString(handler.Namespace) || strings.ContainsFunc(handler.Namespace, unicode.IsControl)) || seen[handler.Namespace] || handler.SchemaVersion == 0 || !exportName.MatchString(handler.Export) {
			return true, errors.New("declare unique namespaces, positive schema versions, and named handler exports")
		}
		seen[handler.Namespace] = true
		if handler.Client != nil {
			needsClient = true
			if !exportName.MatchString(handler.Client.Export) {
				return true, errors.New("client contracts require a named export")
			}
			if _, err := readModule(root, handler.Client.Module); err != nil {
				return true, errors.New("client contract module must be a regular JavaScript file inside the project")
			}
		}
		if _, err := readModule(root, handler.Module); err != nil {
			return true, errors.New("handler modules must be existing regular JavaScript files inside the project")
		}
	}
	manifest, err := readFile(root, "package.json", 1<<20)
	var npm struct {
		Dependencies map[string]string `json:"dependencies"`
	}
	if err != nil || json.Unmarshal(manifest, &npm) != nil || npm.Dependencies["@gregale/sdk-node"] != "file:packages/gregale-sdk.tgz" {
		return true, errors.New("declare @gregale/sdk-node as file:packages/gregale-sdk.tgz")
	}
	integrity, err := checkSDK(root, needsClient)
	if err != nil {
		return true, err
	}
	lock, err := readFile(root, "package-lock.json", 4<<20)
	var pinned struct {
		Packages map[string]struct {
			Dependencies map[string]string `json:"dependencies"`
			Resolved     string            `json:"resolved"`
			Integrity    string            `json:"integrity"`
		} `json:"packages"`
	}
	if err != nil || json.Unmarshal(lock, &pinned) != nil || pinned.Packages[""].Dependencies["@gregale/sdk-node"] != npm.Dependencies["@gregale/sdk-node"] || pinned.Packages["node_modules/@gregale/sdk-node"].Resolved != "file:packages/gregale-sdk.tgz" || pinned.Packages["node_modules/@gregale/sdk-node"].Integrity != integrity {
		return true, errors.New("install the packaged SDK and retain its npm package-lock.json")
	}
	if config.RestoreValidator != "" {
		if config.RestoreValidator != "gregale.validator.json" {
			return true, errors.New("restore_validator must name gregale.validator.json")
		}
		body, err := readFile(root, config.RestoreValidator, api.MaxDurableEntityValidatorRegistryBytes)
		var source validatorbundle.Source
		if err != nil || strict(body, &source) != nil || (source.Runtime != api.ExecutionRuntimeNode22 && source.Runtime != api.ExecutionRuntimeNode24) || api.ValidateExecutionBundle(source.Entrypoint, source.Files, api.MaxDurableEntityValidatorRegistryBytes) != nil {
			return true, errors.New("prepare a valid explicit Node restore-validator source bundle")
		}
		for _, file := range source.Files {
			current, err := readFile(root, file.Path, api.MaxDurableEntityValidatorRegistryBytes)
			if err != nil || !bytes.Equal(current, file.Content) {
				return true, errors.New("restore-validator bundle is stale; regenerate after changing its declared source files")
			}
		}
	}
	return true, nil
}

func strict(body []byte, destination any) error {
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return err
	}
	if decoder.Decode(new(any)) != io.EOF {
		return errors.New("unexpected trailing JSON")
	}
	return nil
}

func readModule(root, name string) ([]byte, error) {
	if !strings.HasSuffix(name, ".mjs") && !strings.HasSuffix(name, ".js") {
		return nil, errors.New("compiled JavaScript module required")
	}
	return readFile(root, name, 1<<20)
}

// Reject absolute paths, traversal and symlink components; do not follow a source
// declaration into a developer's secret store. Callers receive no source contents.
func regularFile(root, name string) (string, error) {
	if name == "" || filepath.IsAbs(name) || strings.Contains(name, "\\") || filepath.ToSlash(filepath.Clean(name)) != name || name == "." || name == ".." || strings.HasPrefix(name, "../") {
		return "", errors.New("invalid project file path")
	}
	current := root
	parts := strings.Split(name, "/")
	for index, part := range parts {
		current = filepath.Join(current, part)
		info, err := os.Lstat(current)
		if err != nil || info.Mode()&os.ModeSymlink != 0 || (index < len(parts)-1 && !info.IsDir()) || (index == len(parts)-1 && !info.Mode().IsRegular()) {
			return "", errors.New("regular project file required")
		}
	}
	return current, nil
}

func readFile(root, name string, limit int) ([]byte, error) {
	path, err := regularFile(root, name)
	if err != nil {
		return nil, err
	}
	f, err := os.Open(path) //nolint:forbidigo // regularFile rejects symlink components and non-regular developer source files before bounded reads.
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	body, err := io.ReadAll(io.LimitReader(f, int64(limit)+1))
	if err != nil || len(body) > limit {
		return nil, errors.New("project file exceeds byte limit")
	}
	return body, nil
}

func checkSDK(root string, needsClient bool) (string, error) {
	body, err := readFile(root, "packages/gregale-sdk.tgz", 32<<20)
	if err != nil {
		return "", errors.New("copy a bounded built SDK package to packages/gregale-sdk.tgz")
	}
	sum := sha512.Sum512(body)
	integrity := "sha512-" + base64.StdEncoding.EncodeToString(sum[:])
	zipped, err := gzip.NewReader(bytes.NewReader(body))
	if err != nil {
		return "", errors.New("packaged SDK must be an npm tar.gz")
	}
	defer func() { _ = zipped.Close() }()
	limited := &io.LimitedReader{R: zipped, N: 128 << 20}
	archive := tar.NewReader(limited)
	manifest := false
	runtime := false
	client := false
	for entries := 0; entries < api.SourceArchiveMaxEntries; entries++ {
		header, err := archive.Next()
		if errors.Is(err, io.EOF) && manifest && runtime && (!needsClient || client) && limited.N > 0 {
			return integrity, nil
		}
		if err != nil || limited.N <= 0 {
			break
		}
		if header.Name == "package/dist/durable-entity-client.js" && header.Typeflag == tar.TypeReg {
			client = true
		}
		if header.Name == "package/dist/durable-entity-runtime.js" && header.Typeflag == tar.TypeReg {
			runtime = true
		}
		if header.Name != "package/package.json" {
			continue
		}
		if manifest || header.Typeflag != tar.TypeReg || header.Size > 1<<20 {
			break
		}
		body, err := io.ReadAll(io.LimitReader(archive, (1<<20)+1))
		var metadata struct {
			Name    string `json:"name"`
			Exports map[string]struct {
				Import string `json:"import"`
			} `json:"exports"`
		}
		if err != nil || json.Unmarshal(body, &metadata) != nil || metadata.Name != "@gregale/sdk-node" || metadata.Exports["./durable-entities/runtime"].Import != "./dist/durable-entity-runtime.js" || metadata.Exports["./durable-entities/testing"].Import != "./dist/durable-entity-testing.js" {
			break
		}
		if needsClient && metadata.Exports["./durable-entities/client"].Import != "./dist/durable-entity-client.js" {
			break
		}
		manifest = true
	}
	return "", errors.New("repack an SDK with the required durable-entity runtime, testing and declared client exports")
}
