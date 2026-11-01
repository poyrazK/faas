// adr: 20261031095954 — deploy the Data API runtime the control plane serves.
package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path"
	"slices"
	"sort"
	"strings"

	"github.com/onebox-faas/faas/cmd/gregale/templates"
	"github.com/onebox-faas/faas/pkg/api"
)

// dataAPIRuntimeBundle is the runtime one deploy uploads.
type dataAPIRuntimeBundle struct {
	Version string
	// Source is "control_plane", or "cli" when apid predates the runtime route.
	Source string
	Files  map[string][]byte
}

func embeddedDataAPIRuntime() (dataAPIRuntimeBundle, error) {
	version, err := templates.DataAPIRuntimeVersion()
	if err != nil {
		return dataAPIRuntimeBundle{}, err
	}
	files, err := templates.DataAPIRuntimeFiles()
	if err != nil {
		return dataAPIRuntimeBundle{}, err
	}
	return dataAPIRuntimeBundle{Version: version, Source: "cli", Files: files}, nil
}

// resolveDataAPIRuntime returns the runtime the control plane serves. Only an
// apid that does not offer the route (an older release) falls back to the
// runtime this CLI carries; any other failure stops the deploy instead of
// shipping a runtime the platform did not choose.
func resolveDataAPIRuntime(ctx context.Context, client *api.Client, slug string) (dataAPIRuntimeBundle, error) {
	served, err := client.GetDataAPIRuntime(ctx, slug)
	if routeNotOffered(err) {
		return embeddedDataAPIRuntime()
	}
	if err != nil {
		return dataAPIRuntimeBundle{}, fmt.Errorf("download the Data API runtime: %w", err)
	}
	files, err := readDataAPIRuntimeArchive(served.Archive)
	if err != nil {
		return dataAPIRuntimeBundle{}, fmt.Errorf("read the Data API runtime: %w", err)
	}
	if got := dataAPIRuntimeFileVersion(files); got != served.Version {
		return dataAPIRuntimeBundle{}, fmt.Errorf("the Data API runtime archive names version %q, the control plane %q", got, served.Version)
	}
	return dataAPIRuntimeBundle{Version: served.Version, Source: "control_plane", Files: files}, nil
}

// readDataAPIRuntimeArchive unpacks a runtime archive into memory: regular
// files under the runtime root only, with clean relative names, within the
// runtime size limit.
func readDataAPIRuntimeArchive(archive []byte) (map[string][]byte, error) {
	gz, err := gzip.NewReader(bytes.NewReader(archive))
	if err != nil {
		return nil, err
	}
	tr := tar.NewReader(gz)
	files := map[string][]byte{}
	var total int64
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, err
		}
		name, ok := strings.CutPrefix(hdr.Name, templates.DataAPIRuntimeArchiveRoot+"/")
		if !ok || hdr.Typeflag != tar.TypeReg || !cleanDataAPIRuntimePath(name) {
			return nil, fmt.Errorf("unexpected runtime entry %q", hdr.Name)
		}
		if _, dup := files[name]; dup {
			return nil, fmt.Errorf("duplicate runtime entry %q", hdr.Name)
		}
		total += hdr.Size
		if hdr.Size < 0 || total > api.DataAPIRuntimeFilesMaxBytes {
			return nil, fmt.Errorf("runtime files exceed %d bytes", api.DataAPIRuntimeFilesMaxBytes)
		}
		content, err := io.ReadAll(io.LimitReader(tr, hdr.Size))
		if err != nil {
			return nil, err
		}
		files[name] = content
	}
	for _, required := range []string{"Dockerfile", "Procfile", templates.DataAPIRuntimeVersionFile} {
		if _, ok := files[required]; !ok {
			return nil, fmt.Errorf("runtime archive has no %s", required)
		}
	}
	return files, nil
}

func cleanDataAPIRuntimePath(name string) bool {
	if name == "" || path.IsAbs(name) || path.Clean(name) != name || name == ".." || strings.HasPrefix(name, "../") || strings.Contains(name, "\\") {
		return false
	}
	for _, part := range strings.Split(name, "/") {
		if strings.HasPrefix(part, ".") {
			return false
		}
	}
	return true
}

func dataAPIRuntimeFileVersion(files map[string][]byte) string {
	var file struct {
		Version string `json:"version"`
	}
	if json.Unmarshal(files[templates.DataAPIRuntimeVersionFile], &file) != nil {
		return ""
	}
	return file.Version
}

// dataAPIRuntimeArchiveFiles lists the runtime files in archive order.
func dataAPIRuntimeArchiveFiles(runtime dataAPIRuntimeBundle, skip ...string) []dataAPIArchiveFile {
	files := make([]dataAPIArchiveFile, 0, len(runtime.Files))
	for name, content := range runtime.Files {
		if !slices.Contains(skip, name) {
			files = append(files, dataAPIArchiveFile{name: name, mode: 0o644, content: content})
		}
	}
	sort.Slice(files, func(i, j int) bool { return files[i].name < files[j].name })
	return files
}

// deployDataAPIRuntime uploads the runtime alone, for an API whose schema is
// managed elsewhere.
func deployDataAPIRuntime(ctx context.Context, client *api.Client, slug, scope, timeout string) int {
	runtime, err := resolveDataAPIRuntime(ctx, client, slug)
	if err != nil {
		return printErr("Could not get the Data API runtime", err)
	}
	reportDataAPIRuntime(runtime)
	return deployDataAPIArchive(ctx, slug, scope, timeout, func(dest string) error {
		return writeDataAPIArchive(dest, dataAPIRuntimeArchiveFiles(runtime))
	})
}

func reportDataAPIRuntime(runtime dataAPIRuntimeBundle) {
	if jsonOutput {
		return
	}
	if runtime.Source == "cli" {
		PrintProgress(osStdout, "Data API runtime %s (from this CLI: the control plane does not serve one yet)", runtime.Version)
		return
	}
	PrintProgress(osStdout, "Data API runtime %s", runtime.Version)
}

// dataAPIHealthRuntime compares the runtime a Data API reports with the one
// the control plane serves.
type dataAPIHealthRuntime struct {
	// Running is the reported version, or "unknown" for a runtime that
	// predates version reporting.
	Running string `json:"running"`
	Current string `json:"current,omitempty"`
	// State is current, not_current, or unknown when either side is unknown.
	State string `json:"state"`
}

// currentDataAPIRuntimeVersion returns the version the control plane serves,
// or "" when it cannot say (an older apid, or a failed read).
func currentDataAPIRuntimeVersion(ctx context.Context, client *api.Client, slug string) string {
	served, err := client.GetDataAPIRuntime(ctx, slug)
	if err != nil {
		return ""
	}
	return served.Version
}

// attachDataAPIRuntime records the comparison. A runtime that is not current
// is a finding but leaves the status alone: it serves, it is only older.
func attachDataAPIRuntime(report *dataAPIHealthReport, current string) {
	runtime := dataAPIHealthRuntime{Running: report.Readiness.RuntimeVersion, Current: current, State: "unknown"}
	if runtime.Running == "" {
		runtime.Running = "unknown"
	}
	observed := report.Readiness.State == "ok" || report.Readiness.State == "unready"
	switch {
	case current == "" || !observed:
	case runtime.Running == current:
		runtime.State = "current"
	default:
		runtime.State = "not_current"
		report.CurrentFindings = append(report.CurrentFindings, "runtime_not_current")
	}
	report.Runtime = &runtime
}

func writeDataAPIHealthRuntime(slug string, runtime *dataAPIHealthRuntime) {
	if runtime == nil || runtime.State == "unknown" {
		return
	}
	if runtime.State == "current" {
		_, _ = fmt.Fprintf(osStdout, "Runtime: %s (current)\n", runtime.Running)
		return
	}
	_, _ = fmt.Fprintf(osStdout, "Runtime: %s; the platform serves %s. Upgrade with: gregale data-api deploy %s\n", runtime.Running, runtime.Current, slug)
}
