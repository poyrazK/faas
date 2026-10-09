// adr: 856
// Offline release hook: packages explicit files without executing customer code.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/durableentity/validatorbundle"
)

type paths []string

func (p *paths) String() string         { return strings.Join(*p, ",") }
func (p *paths) Set(value string) error { *p = append(*p, value); return nil }

func main() {
	if err := run(os.Args[1:], os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(args []string, out io.Writer) error {
	fs := flag.NewFlagSet("durable-validator-registry", flag.ContinueOnError)
	publish := fs.String("publish-registry", "", "publish an existing reviewed registry to the configured private artifact bucket")
	sourceOnly := fs.Bool("source-bundle", false, "package gregale.validator.json for automatic publication by source builds")
	var files paths
	app := fs.String("app", "", "application UUID")
	deployment := fs.String("deployment", "", "deployment UUID")
	runtime := fs.String("runtime", "", "node22, node24, python312 or python313")
	root := fs.String("root", "", "reviewed validator source directory")
	entry := fs.String("entrypoint", "", "relative entrypoint file")
	registry := fs.String("registry", "", "optional existing registry input")
	output := fs.String("output", "", "new registry artifact path; must not exist")
	fs.Var(&files, "file", "explicit relative source file; repeat for dependencies")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *publish != "" {
		if *sourceOnly || *app != "" || *deployment != "" || *runtime != "" || *root != "" || *entry != "" || *registry != "" || *output != "" || len(files) != 0 || fs.NArg() != 0 {
			return fmt.Errorf("publish-registry accepts only a reviewed registry path")
		}
		return publishRegistry(*publish, out)
	}
	if *sourceOnly {
		if *app != "" || *deployment != "" || *registry != "" || !api.ExecutionRuntime(*runtime).Valid() || *root == "" || *output == "" || fs.NArg() != 0 {
			return fmt.Errorf("source-bundle requires runtime, root, entrypoint, files and new output; no deployment identity")
		}
		bundle, err := packageBundle(*root, *entry, files)
		if err != nil {
			return err
		}
		bundle.Runtime = api.ExecutionRuntime(*runtime)
		body, err := json.Marshal(validatorbundle.Source{Runtime: bundle.Runtime, Entrypoint: bundle.Entrypoint, Files: bundle.Files})
		if err != nil || len(body) > api.MaxDurableEntityValidatorRegistryBytes {
			return validatorbundle.ErrArtifactUnavailable
		}
		if err = publishArtifact(*output, body); err != nil {
			return err
		}
		_, err = fmt.Fprintf(out, "source_bundle_sha256=%s files=%d\n", validatorbundle.Hash(bundle), len(bundle.Files))
		return err
	}
	appID, e1 := uuid.Parse(*app)
	deploymentID, e2 := uuid.Parse(*deployment)
	if e1 != nil || e2 != nil || appID == uuid.Nil || deploymentID == uuid.Nil || appID.String() != *app || deploymentID.String() != *deployment || !api.ExecutionRuntime(*runtime).Valid() || *root == "" || *output == "" || fs.NArg() != 0 {
		return fmt.Errorf("explicit canonical app/deployment UUIDs, runtime, root and new output path are required")
	}
	bundle, err := packageBundle(*root, *entry, files)
	if err != nil {
		return err
	}
	bundle.AppID, bundle.DeploymentID, bundle.Runtime = *app, *deployment, api.ExecutionRuntime(*runtime)
	bundle.SHA256 = validatorbundle.Hash(bundle)
	existing := map[string]validatorbundle.Bundle{}
	if *registry != "" {
		existing, err = validatorbundle.Load(*registry)
		if err != nil {
			return err
		}
	}
	body, err := validatorbundle.Merge(existing, bundle)
	if err != nil {
		return err
	}
	if err = publishArtifact(*output, body); err != nil {
		return err
	}
	_, err = fmt.Fprintf(out, "deployment=%s bundle_sha256=%s files=%d\n", bundle.DeploymentID, bundle.SHA256, len(bundle.Files))
	return err
}

func packageBundle(root, entry string, names []string) (validatorbundle.Bundle, error) {
	b := validatorbundle.Bundle{Entrypoint: entry}
	if len(names) == 0 || len(names) > api.ExecutionBundleMaxFiles {
		return b, fmt.Errorf("provide an explicit bounded validator file list")
	}
	sort.Strings(names)
	rootFS, err := os.OpenRoot(root)
	if err != nil {
		return b, fmt.Errorf("validator root unavailable")
	}
	defer func() { _ = rootFS.Close() }()
	total := 0
	for _, name := range names {
		// Validate normalized paths before accessing the filesystem.
		if name == "" || filepath.IsAbs(name) || strings.Contains(name, "\\") || filepath.ToSlash(filepath.Clean(name)) != name || name == ".." || strings.HasPrefix(name, "../") {
			return b, fmt.Errorf("validator paths must be normalized relative files")
		}
		path := root
		parts := strings.Split(name, "/")
		for index, part := range parts {
			path = filepath.Join(path, part)
			info, err := os.Lstat(path)
			if err != nil || info.Mode()&os.ModeSymlink != 0 || (index == len(parts)-1 && !info.Mode().IsRegular()) {
				return b, fmt.Errorf("validator files must exist and contain no symlinks")
			}
		}
		f, err := rootFS.Open(name)
		if err != nil {
			return b, fmt.Errorf("validator file unavailable")
		}
		info, err := f.Stat()
		if err != nil || !info.Mode().IsRegular() {
			_ = f.Close()
			return b, fmt.Errorf("validator source must contain regular files")
		}
		body, err := io.ReadAll(io.LimitReader(f, int64(api.MaxDurableEntityValidatorRegistryBytes-total)+1))
		_ = f.Close()
		if err != nil || len(body) > api.MaxDurableEntityValidatorRegistryBytes-total {
			return b, fmt.Errorf("validator source exceeds registry byte limit")
		}
		total += len(body)
		b.Files = append(b.Files, api.ExecutionFile{Path: name, Content: body})
	}
	if err := api.ValidateExecutionBundle(entry, b.Files, api.MaxDurableEntityValidatorRegistryBytes); err != nil {
		return b, fmt.Errorf("invalid validator source bundle: %w", err)
	}
	return b, nil
}

func publishArtifact(path string, body []byte) error {
	f, err := os.CreateTemp(filepath.Dir(path), ".validator-registry-*")
	if err != nil {
		return err
	}
	temporary := f.Name()
	defer func() { _ = os.Remove(temporary) }()
	if _, err = f.Write(body); err != nil {
		_ = f.Close()
		return err
	}
	if err = f.Sync(); err != nil {
		_ = f.Close()
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	// Hard link publishes a complete file atomically without replacing an input
	// registry or an existing release artifact. Temp files are mode 0600.
	return os.Link(temporary, path)
}

func publishRegistry(path string, out io.Writer) error {
	entries, err := validatorbundle.Load(path)
	if err != nil {
		return err
	}
	artifacts, err := validatorbundle.OpenArtifacts(os.Getenv)
	if err != nil || artifacts == nil {
		return validatorbundle.ErrArtifactUnavailable
	}
	ctx, cancel := context.WithTimeout(context.Background(), api.DurableEntityInvokeTimeout)
	defer cancel()
	ids := make([]string, 0, len(entries))
	for id := range entries {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		if err := artifacts.Publish(ctx, entries[id]); err != nil {
			return err
		}
		if _, err := fmt.Fprintf(out, "published deployment=%s bundle_sha256=%s\n", id, entries[id].SHA256); err != nil {
			return err
		}
	}
	return nil
}
