package issues

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"syscall"

	"github.com/onebox-faas/faas/pkg/api"
)

// ValidateSourceMapBuildConfig intentionally starts with Node's /app tree.
// Requiring a canonical path also makes the host/guest/cache comparison exact.
func ValidateSourceMapBuildConfig(in api.IssueSourceMapBuildConfig) error {
	if in.Version != 1 || (in.Directory != "/app" && !strings.HasPrefix(in.Directory, "/app/")) ||
		path.Clean(in.Directory) != in.Directory || strings.ContainsAny(in.Directory, "\\:\x00") || len(in.Directory) > api.IssueMaxFrameBytes {
		return errors.New("source-map build config requires version 1 and a canonical directory under /app")
	}
	return nil
}

// ReadSourceMapBuildFile never follows links or opens a device/FIFO. The root
// confines all reads even if a directory changes between Lstat and OpenFile.
func ReadSourceMapBuildFile(root *os.Root, name string, maximum int) ([]byte, error) {
	if !fs.ValidPath(name) || strings.ContainsAny(name, "\\\x00") {
		return nil, errors.New("invalid build artifact path")
	}
	parts := strings.Split(name, "/")
	for i := range parts {
		info, err := root.Lstat(strings.Join(parts[:i+1], "/"))
		if err != nil {
			return nil, err
		}
		if (i < len(parts)-1 && !info.IsDir()) || (i == len(parts)-1 && !info.Mode().IsRegular()) {
			return nil, errors.New("build artifact must be regular and have no symlink components")
		}
	}
	f, err := root.OpenFile(name, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	before, err := f.Stat()
	if err != nil || !before.Mode().IsRegular() || before.Size() > int64(maximum) {
		return nil, errors.New("build artifact exceeds its byte bound or is not regular")
	}
	data, err := io.ReadAll(io.LimitReader(f, int64(maximum)+1))
	if err != nil {
		return nil, err
	}
	after, err := f.Stat()
	if err != nil || len(data) > maximum || int64(len(data)) != before.Size() || after.Size() != before.Size() || !after.ModTime().Equal(before.ModTime()) {
		return nil, errors.New("build artifact changed while reading")
	}
	return data, nil
}

// DecodeSourceMapBuildJSON rejects trailing JSON and unknown binding/config
// fields. It never includes customer file contents in diagnostics.
func DecodeSourceMapBuildJSON(data []byte, out any) error {
	d := json.NewDecoder(bytes.NewReader(data))
	d.DisallowUnknownFields()
	if d.Decode(out) != nil || !errors.Is(d.Decode(new(any)), io.EOF) {
		return errors.New("invalid source-map build metadata")
	}
	return nil
}

// DiscoverBuildSourceMaps reads ONLY the final filesystem exported by BuildKit
// inside the builder VM. It executes nothing and never fetches map URLs.
func DiscoverBuildSourceMaps(root *os.Root, cfg api.IssueSourceMapBuildConfig) (*SourceMapSet, error) {
	if err := ValidateSourceMapBuildConfig(cfg); err != nil {
		return nil, err
	}
	dir := strings.TrimPrefix(cfg.Directory, "/")
	files, err := sourceMapBuildBundles(root, dir)
	if err != nil {
		return nil, fmt.Errorf("discover source-map bundles: %w", err)
	}
	in := api.PublishIssueSourceMapsRequest{Directory: cfg.Directory, Manifest: api.IssueSourceMapManifest{Version: 1}}
	bundleBytes, mapBytes := 0, 0
	for _, file := range files {
		bundle, err := ReadSourceMapBuildFile(root, dir+"/"+file, min(api.IssueSourceMapBundleMaxBytes, api.IssueSourceMapBundlesMaxBytes-bundleBytes))
		if err != nil {
			return nil, fmt.Errorf("read source-map bundle: %w", err)
		}
		bundleBytes += len(bundle)
		mapFile, explicit, err := sourceMapBuildReference(bundle, file)
		if err != nil {
			return nil, err
		}
		payload, err := ReadSourceMapBuildFile(root, dir+"/"+mapFile, api.IssueSourceMapMaxBytes)
		if errors.Is(err, fs.ErrNotExist) && !explicit {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("read source map: %w", err)
		}
		mapBytes += len(payload)
		if mapBytes > api.IssueSourceMapsMaxBytes || len(in.Manifest.Artifacts) == api.IssueSourceMapsMax {
			return nil, errors.New("source maps exceed the build discovery bounds")
		}
		payload, err = portableBuildSourceMap(payload, file, mapFile)
		if err != nil {
			return nil, err
		}
		digest := sha256.Sum256(bundle)
		in.Manifest.Artifacts = append(in.Manifest.Artifacts, api.IssueSourceMapArtifact{GeneratedFile: file, SHA256: hex.EncodeToString(digest[:]), Payload: string(payload)})
	}
	return CompileSourceMaps(in) // Also rejects empty discovery.
}

func sourceMapBuildBundles(root *os.Root, dir string) ([]string, error) {
	// Check every component before opening the discovery root: os.Root permits
	// contained symlinks, but source-map discovery deliberately does not.
	for current := dir; current != "."; current = path.Dir(current) {
		st, err := root.Lstat(current)
		if err != nil || !st.IsDir() {
			return nil, errors.New("source-map directory must exist without symlink components")
		}
	}
	pending := []string{dir}
	files := []string{}
	visited := 0
	for len(pending) > 0 {
		current := pending[len(pending)-1]
		pending = pending[:len(pending)-1]
		d, err := root.Open(current)
		if err != nil {
			return nil, err
		}
		err = func() error {
			defer func() { _ = d.Close() }()
			for {
				entries, err := d.ReadDir(1)
				if errors.Is(err, io.EOF) {
					return nil
				}
				if err != nil {
					return err
				}
				for _, e := range entries {
					visited++
					if visited > api.IssueSourceMapDiscoveryEntriesMax {
						return errors.New("source-map tree exceeds its entry bound")
					}
					if e.Type()&os.ModeSymlink != 0 || e.Name() == "node_modules" || e.Name() == ".git" {
						continue
					}
					file := current + "/" + e.Name()
					if e.IsDir() {
						pending = append(pending, file)
					} else if e.Type().IsRegular() && slices.Contains([]string{".js", ".mjs", ".cjs"}, path.Ext(e.Name())) {
						files = append(files, strings.TrimPrefix(file, dir+"/"))
					}
				}
			}
		}()
		if err != nil {
			return nil, err
		}
	}
	slices.Sort(files)
	return files, nil
}

var buildSourceMapComments = regexp.MustCompile(`(?m)^\s*//[#@]\s*sourceMappingURL=([^\s]+)\s*$|/\*[#@]\s*sourceMappingURL=([^\s]+)\s*\*/`)

func sourceMapBuildReference(bundle []byte, generated string) (string, bool, error) {
	matches := buildSourceMapComments.FindAllSubmatch(bundle, -1)
	if len(matches) == 0 {
		return generated + ".map", false, nil
	}
	last := matches[len(matches)-1]
	reference := string(last[1])
	if len(last[2]) != 0 {
		reference = string(last[2])
	}
	u, err := url.Parse(reference)
	if err != nil || u.IsAbs() || u.Host != "" || u.RawQuery != "" || u.Fragment != "" || u.ForceQuery || u.User != nil || strings.HasPrefix(u.Path, "/") || strings.ContainsAny(u.Path, "\\:\x00") || u.Path == "" {
		return "", false, errors.New("sourceMappingURL must name a plain relative local map")
	}
	file := path.Join(path.Dir(generated), u.Path)
	if !portableSourceMapFile(file) {
		return "", false, errors.New("source map escapes the bundle directory")
	}
	return file, true, nil
}

func portableBuildSourceMap(payload []byte, generated, mapFile string) ([]byte, error) {
	var m flatSourceMap
	if json.Unmarshal(payload, &m) != nil || m.Version != 3 || m.Sections != nil || m.Sources == nil {
		return nil, errSourceMap
	}
	absolute := func(s string) bool {
		u, err := url.Parse(s)
		return err != nil || strings.HasPrefix(s, "/") || strings.Contains(s, "\\") || strings.EqualFold(u.Scheme, "file") || (len(s) > 1 && s[1] == ':')
	}
	if absolute(m.SourceRoot) {
		return nil, errors.New("source-map builds require portable source paths")
	}
	for _, source := range m.Sources {
		if absolute(source) {
			return nil, errors.New("source-map builds require portable source paths")
		}
	}
	u, _ := url.Parse(m.SourceRoot)
	if u.Scheme == "" {
		rel, err := filepath.Rel(path.Dir(generated), path.Dir(mapFile))
		if err != nil {
			return nil, err
		}
		m.SourceRoot = path.Join(filepath.ToSlash(rel), m.SourceRoot) + "/"
	}
	// flatSourceMap deliberately omits sourcesContent and producer extensions.
	return json.Marshal(m)
}
