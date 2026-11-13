package main

import (
	"archive/tar"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
)

type deploySourceFile struct {
	Path           string `json:"path"`
	ArchivePath    string `json:"archive_path,omitempty"`
	Included       bool   `json:"included"`
	Kind           string `json:"kind"`
	Bytes          int64  `json:"bytes"`
	Reason         string `json:"reason"`
	RuleFile       string `json:"rule_file,omitempty"`
	RuleLine       int    `json:"rule_line,omitempty"`
	ExcludedParent string `json:"excluded_parent,omitempty"`
	Symlink        bool   `json:"symlink,omitempty"`
	Generated      bool   `json:"generated,omitempty"`
	Redacted       bool   `json:"redacted,omitempty"`
	CredentialLike bool   `json:"credential_like,omitempty"`
}

type deploySourceFilesReport struct {
	SchemaVersion       int                `json:"schema_version"`
	SourceMode          string             `json:"source_mode"`
	Commit              string             `json:"commit,omitempty"`
	SelectedPath        string             `json:"selected_path"`
	ContextRoot         string             `json:"context_root"`
	SourceRoot          string             `json:"source_root,omitempty"`
	FileCount           int                `json:"file_count"`
	IncludedBytes       int64              `json:"included_bytes"`
	CompressedBytes     int64              `json:"compressed_bytes"`
	AccountLimitChecked bool               `json:"account_limit_checked"`
	Note                string             `json:"note"`
	Files               []deploySourceFile `json:"files"`
	selected            map[string]deploySourceFile
	generated           map[string][]byte
	redacted            map[string]bool
	archiveRoot         string
}

func newDeploySourceFilesReport() *deploySourceFilesReport {
	return &deploySourceFilesReport{SchemaVersion: 1, Files: []deploySourceFile{}, selected: map[string]deploySourceFile{}, redacted: map[string]bool{},
		Note: "Local payload inspection; account upload limits, remote configuration and runtime readiness are not checked. Excluded directories represent pruned subtrees. HEAD uses Git's normalized archive, without uncommitted, untracked or export-ignored files."}
}

// observe is called by the actual directory/archive packer, never by a second
// approximation of its walk. Excluded directories need no descendant scan.
func (r *deploySourceFilesReport) observe(name string, dir bool, size int64, decision packSelection, symlink bool) error {
	if r == nil {
		return nil
	}
	if _, exists := r.selected[name]; !exists && len(r.selected) >= api.SourceArchiveMaxEntries {
		return fmt.Errorf("file explanation exceeds the source archive entry bound (%d)", api.SourceArchiveMaxEntries)
	}
	kind := "file"
	if dir {
		kind, size = "directory", 0
	}
	r.selected[name] = deploySourceFile{Path: name, Kind: kind, Bytes: size, Included: !decision.excluded, Reason: decision.reason,
		RuleFile: decision.file, RuleLine: decision.line, ExcludedParent: decision.ancestor, Symlink: symlink}
	return nil
}

// Read final headers so generated and redacted bytes, transport prefixes and
// file counts describe the same archive the deploy packer actually produced.
func (r *deploySourceFilesReport) finish(archive string) error {
	seen := map[string]bool{}
	err := forEachArchiveEntry(archive, func(hdr *tar.Header, _ io.Reader) error {
		if hdr.Typeflag != tar.TypeReg && hdr.Typeflag != tar.TypeDir {
			return nil
		}
		name := strings.TrimSuffix(hdr.Name, "/")
		if r.archiveRoot != "" {
			name = strings.TrimPrefix(name, r.archiveRoot+"/")
		}
		row, exists := r.selected[name]
		if !exists {
			if _, generated := r.generated[name]; !generated {
				return fmt.Errorf("unexplained archive entry %q", name)
			}
			row = deploySourceFile{Path: name, Included: true, Kind: "file", Generated: true, Reason: "generated Go function build marker"}
		}
		if !row.Included || seen[name] {
			return fmt.Errorf("archive disagrees with file selection for %q", name)
		}
		seen[name] = true
		row.ArchivePath, row.Bytes, row.Redacted = hdr.Name, hdr.Size, r.redacted[name]
		row.CredentialLike = row.Kind == "file" && credentialFileName(name)
		r.selected[name] = row
		if hdr.Typeflag == tar.TypeReg {
			r.FileCount++
			r.IncludedBytes += hdr.Size
		}
		return nil
	})
	if err != nil {
		return err
	}
	for name, row := range r.selected {
		if row.Included && !seen[name] {
			return fmt.Errorf("selected path %q is missing from archive", name)
		}
		r.Files = append(r.Files, row)
	}
	sort.Slice(r.Files, func(i, j int) bool { return r.Files[i].Path < r.Files[j].Path })
	info, err := os.Stat(archive)
	if err != nil {
		return err
	}
	r.CompressedBytes = info.Size()
	return nil
}

type deploySourceFilesOptions struct {
	path, source, runtime, secretScan, secretsFile string
	worktree, function, app, project, dryRun, json bool
}

func cmdDeploySourceFiles(ctx context.Context, o deploySourceFilesOptions, explicit map[string]bool) int {
	if err := validateDeploySourceFiles(o, explicit); err != nil {
		return printErr("Invalid file explanation", err)
	}
	report, archive, cleanup, err := prepareDeploySourceFiles(ctx, o, explicit["source"])
	if err != nil {
		return printErr("Could not inspect deploy files", err)
	}
	defer cleanup()
	if err := report.finish(archive); err != nil {
		return printErr("Could not inspect packed files", err)
	}
	if o.json {
		return jsonOut(writeJSON(report))
	}
	_, _ = fmt.Fprintf(osStdout, "Source: %s", report.SourceMode)
	if report.Commit != "" {
		_, _ = fmt.Fprintf(osStdout, " (%s)", report.Commit)
	}
	_, _ = fmt.Fprintf(osStdout, "\n%d files, %d source bytes, %d compressed bytes\n", report.FileCount, report.IncludedBytes, report.CompressedBytes)
	for _, row := range report.Files {
		action := "include"
		if !row.Included {
			action = "exclude"
		}
		rule := row.Reason
		if row.RuleFile != "" {
			rule += fmt.Sprintf(" (%s:%d)", row.RuleFile, row.RuleLine)
		}
		if row.Symlink {
			rule += "; in-tree symlink dereferenced"
		}
		if row.Redacted {
			rule += "; secret-scan replacement"
		}
		if row.CredentialLike {
			rule += "; credential-like filename"
		}
		_, _ = fmt.Fprintf(osStdout, "%-7s %9d  %s  %s\n", action, row.Bytes, strconv.Quote(row.Path), strconv.Quote(rule))
	}
	_, _ = fmt.Fprintln(osStdout, report.Note)
	return 0
}

func validateDeploySourceFiles(o deploySourceFilesOptions, explicit map[string]bool) error {
	if !o.dryRun {
		return errors.New("--explain-files requires --dry-run")
	}
	allowed := map[string]bool{"dry-run": true, "explain-files": true, "path": true, "source": true, "worktree": true, "json": true,
		"app": true, "function": true, "runtime": true, "handler": true, "secret-scan": true, "secrets-file": true, "project": true, "project-slug": true, "name": true}
	var unsupported []string
	for flag := range explicit {
		if !allowed[flag] {
			unsupported = append(unsupported, "--"+flag)
		}
	}
	if len(unsupported) > 0 {
		sort.Strings(unsupported)
		return fmt.Errorf("local file inspection does not support %s; select a local directory with --path", strings.Join(unsupported, ", "))
	}
	if o.function && o.app {
		return errors.New("--function and --app are mutually exclusive")
	}
	if o.runtime != "" && (!slices.Contains(api.FunctionRuntimes, o.runtime) || o.app) || o.function && o.runtime == "" {
		return errors.New("select a supported function runtime with --function --runtime, or let the source shape be detected")
	}
	return nil
}

func prepareDeploySourceFiles(ctx context.Context, o deploySourceFilesOptions, explicitSource bool) (*deploySourceFilesReport, string, func(), error) {
	if err := ctx.Err(); err != nil {
		return nil, "", nil, err
	}
	root, err := deployContextDirectory(o.path)
	if err != nil {
		return nil, "", nil, err
	}
	mode, err := resolveDeploySourceMode(o.source, explicitSource, o.worktree, false, false)
	if err != nil {
		return nil, "", nil, err
	}
	scan, err := parseSecretScanFlag(o.secretScan)
	if err != nil {
		return nil, "", nil, err
	}
	report := newDeploySourceFilesReport()
	report.SelectedPath, report.ContextRoot = root, root
	workspace := ""
	if o.path != "" {
		if gitRoot, rootErr := gitRootFromCwd(root); rootErr == nil {
			contextRoot, sourceRoot, member, err := resolveWorkspaceContext(gitRoot, root)
			if err != nil {
				return nil, "", nil, err
			}
			if member {
				workspace, report.ContextRoot, report.SourceRoot = contextRoot, contextRoot, sourceRoot
			}
		}
	}
	prov, committed, provErr := resolveZeroConfigProvenance(root)
	if mode == deploySourceHEAD {
		prov, provErr = resolveExplicitHeadProvenance(root)
		committed = provErr == nil
	}
	if provErr != nil && !errors.Is(provErr, ErrNotInGitRepo) && !errors.Is(provErr, ErrNoGitRemote) || mode == deploySourceHEAD && provErr != nil {
		return nil, "", nil, provErr
	}
	if committed && mode != deploySourceWorktree {
		if o.path == "" || workspace != "" {
			report.ContextRoot = prov.Root
		}
		archive, selected, cleanup, err := materializeCommittedGitSource(prov, root, report.SourceRoot, o.path, workspace)
		if err != nil {
			return nil, "", nil, err
		}
		defer cleanup()
		report.SourceMode, report.Commit = "head", prov.SHA
		packed, err := explainCommittedDeployFiles(o, archive, selected, scan, report)
		return report, packed, func() { _ = os.Remove(packed) }, err
	}
	report.SourceMode = "worktree"
	packed, err := explainWorktreeDeployFiles(o, root, scan, report)
	return report, packed, func() { _ = os.Remove(packed) }, err
}

func deployFilesShape(root string, o deploySourceFilesOptions) (shape, string, error) {
	if !o.project {
		_, _, functionSet, err := functionSetManifestFromCode(root)
		if err != nil {
			return shapeUnknown, "", err
		}
		if functionSet {
			return shapeUnknown, "", errors.New("a functions project uploads separate archives; select one function with --path to explain its payload")
		}
	}
	s := detectShape(root)
	if o.app || o.project {
		s = shapeApp
	} else if o.function {
		s = shapeFunction
	}
	if s == shapeUnknown {
		return s, "", errors.New("no deployable local source found; select a workload with --path or use --project for a project archive")
	}
	runtime, _, _ := inferFunctionRuntime(root)
	if o.runtime != "" {
		runtime = o.runtime
	}
	return s, runtime, nil
}

func explainCommittedDeployFiles(o deploySourceFilesOptions, archive, root string, scan secretScanMode, report *deploySourceFilesReport) (string, error) {
	s, runtime, err := deployFilesShape(root, o)
	if err != nil {
		return "", err
	}
	generated, err := gitArchiveGoBuildOnlyFiles(archive, report.SourceRoot, s, runtime)
	if err != nil {
		return "", err
	}
	var excludes []string
	if o.secretsFile != "" {
		for name := range packExtraExcludeSet(report.ContextRoot, []string{o.secretsFile}) {
			excludes = append(excludes, name)
		}
	}
	packed, _, _, err := packGitArchiveReported(archive, largestSourceTarballMB(), scan, generated, dockerfileSource(root), report, excludes...)
	return packed, err
}

func explainWorktreeDeployFiles(o deploySourceFilesOptions, root string, scan secretScanMode, report *deploySourceFilesReport) (string, error) {
	if _, _, err := deployFilesShape(root, o); err != nil {
		return "", err
	}
	overrides, _, err := scanAndRedactEnvFiles(report.ContextRoot, scan)
	if err != nil {
		return "", err
	}
	generated, err := functionGoBuildOnlyFiles(root, report.ContextRoot)
	if err != nil {
		return "", err
	}
	f, err := os.CreateTemp("", "gregale-explain-files-*.tar.gz")
	if err != nil {
		return "", err
	}
	archive := f.Name()
	_ = f.Close()
	archiveRoot := filepath.Base(report.ContextRoot)
	if report.SourceRoot != "" {
		archiveRoot = ""
	}
	var excludes []string
	if o.secretsFile != "" {
		excludes = append(excludes, o.secretsFile)
	}
	_, err = packDirToTarGzReported(report.ContextRoot, archive, largestSourceTarballMB(), overrides, archiveRoot, generated, dockerfileSource(root), report, excludes...)
	if err != nil {
		_ = os.Remove(archive)
		return "", err
	}
	return archive, nil
}
