package main

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func (s *testRehearsalSession) prepare(ctx context.Context, o testRehearsalOptions) error {
	repo, err := rehearsalGitOutput(ctx, o.Repo, "rev-parse", "--show-toplevel")
	if err != nil {
		return err
	}
	s.repo, err = filepath.EvalSymlinks(strings.TrimSpace(repo))
	if err != nil {
		return err
	}
	if err := s.captureInputs(o); err != nil {
		return err
	}
	if err := s.validateEngine(o); err != nil {
		return err
	}
	for side, ref := range []string{o.Base, o.Candidate} {
		revision, err := resolveTestRehearsalRevision(ctx, s.repo, ref)
		if err != nil {
			return fmt.Errorf("resolve revision %q: %w", ref, err)
		}
		if side == 0 {
			s.identity.Baseline = revision
		} else {
			s.identity.Candidate = revision
		}
	}
	for side, revision := range []*testRehearsalRevision{&s.identity.Baseline, &s.identity.Candidate} {
		s.archives[side] = filepath.Join(s.root, fmt.Sprintf("revision-%d.tar.gz", side))
		if err := archiveTestRehearsalRevision(ctx, s.repo, revision.Commit, s.archives[side]); err != nil {
			return err
		}
		revision.ArchiveSHA256, err = tarballSHA256(s.archives[side])
		if err != nil {
			return err
		}
		dir, err := os.MkdirTemp(s.root, "validate-")
		if err != nil {
			return err
		}
		manifestDir, digest, materializeErr := s.materialize(side, dir)
		if materializeErr == nil {
			materializeErr = s.validateRevision(o, manifestDir)
		}
		cleanupErr := os.RemoveAll(dir)
		if materializeErr != nil || cleanupErr != nil {
			return fmt.Errorf("validate revision %s: %w", revision.Commit, errors.Join(materializeErr, cleanupErr))
		}
		revision.SourceSHA256 = digest
	}
	return nil
}

func (s *testRehearsalSession) captureInputs(o testRehearsalOptions) error {
	manifest, err := filepath.Abs(o.Manifest)
	if err != nil {
		return err
	}
	manifestRel, err := rehearsalRelativePath(s.repo, manifest)
	if err != nil {
		return fmt.Errorf("trusted manifest: %w", err)
	}
	s.manifestDir = filepath.Dir(manifestRel)
	file, err := openCustomerFile(manifest)
	if err != nil {
		return err
	}
	body, readErr := io.ReadAll(io.LimitReader(file, (1<<20)+1))
	closeErr := file.Close()
	if readErr != nil || closeErr != nil {
		return errors.Join(readErr, closeErr)
	}
	document, _, err := decodeTestManifestDocument(body, manifest, func(_ string, scenario testScenario) []string { return testHTTPDataFields(scenario) })
	if err != nil {
		return err
	}
	scenario, ok := document.Scenarios[o.Scenario]
	if !ok || scenario.Behavior == nil {
		return errors.New("select a declared scenario with behavior observations")
	}
	s.scenario = scenario
	s.identity.Scenario, s.identity.RecipeSHA256 = o.Scenario, testBehaviorDigest(scenario)
	digest := sha256.Sum256(body)
	s.identity.ManifestSHA256 = hex.EncodeToString(digest[:])
	source := scenario.Source
	if source == "" {
		source = "."
	}
	if filepath.IsAbs(source) {
		return errors.New("rehearsal sources must be relative to the manifest and inside the Git checkout")
	}
	sourceRel, err := rehearsalRelativePath(s.repo, filepath.Join(filepath.Dir(manifest), source))
	if err != nil {
		return err
	}
	names := make(map[string]bool)
	for _, list := range [][]string{scenario.Behavior.Fixtures, scenario.Behavior.Observers} {
		for _, name := range list {
			names[filepath.Join(sourceRel, filepath.FromSlash(name))] = true
		}
	}
	if scenario.ObjectsFixture != nil {
		if err := validateTestObjects(scenario); err != nil {
			return err
		}
		for _, f := range scenario.ObjectsFixture.Fixtures {
			names[filepath.Join(sourceRel, filepath.FromSlash(f.File))] = true
		}
	}
	if scenario.PostgresFixture != nil {
		if err := validateTestPostgres(scenario); err != nil {
			return err
		}
		names[filepath.Join(sourceRel, filepath.FromSlash(scenario.PostgresFixture.Fixture))] = true
	}
	remaining := deployArchiveMaxExpandedBytes
	for name := range names {
		path := filepath.Join(s.repo, name)
		if _, err := rehearsalRelativePath(s.repo, path); err != nil {
			return err
		}
		destination := filepath.Join(s.trusted, name)
		if err := copyTestRehearsalFile(path, destination, min(int64(testBehaviorFileLimit), remaining)); err != nil {
			return err
		}
		info, err := os.Stat(destination)
		if err != nil {
			return err
		}
		remaining -= info.Size()
	}
	s.files, _, err = hashTestRehearsalTree(s.trusted)
	s.identity.TrustedFiles = s.files
	return err
}

func (s *testRehearsalSession) validateEngine(o testRehearsalOptions) error {
	selected := []string{o.Engine}
	if o.Engine == "real-vm" {
		selected, _ = selectedTestProfiles(o.Profile)
	}
	for _, p := range selected {
		if err := validateTestCommandExecution(s.scenario, o.Engine, p); err != nil {
			return err
		}
	}
	if s.scenario.Actions != nil && o.Engine != "real-vm" {
		return errors.New("webhook action capture requires the real-vm engine")
	}
	if o.Engine == "local" {
		if err := validateLocalTestScenario(s.scenario); err != nil {
			return err
		}
		if s.scenario.Local == nil {
			return errors.New("local rehearsals require local.command to start each revision in a fresh process")
		}
	}
	if o.Engine == "simulated" && len(s.scenario.Simulation) == 0 {
		return errors.New("simulated rehearsals require a simulation command")
	}
	if o.Engine != "real-vm" && (s.scenario.Chaos != nil || len(s.scenario.Steps) > 0) {
		return errors.New("staged scenarios and managed faults require the real-vm engine")
	}
	profiles, _ := selectedTestProfiles(o.Profile)
	if o.Engine == "real-vm" && estimateTestWorkloadMinutes(s.scenario, 2*o.Repeat*len(profiles)) > o.MaxWorkloadMinutes {
		return errors.New("both revisions exceed --max-workload-minutes; select fewer profiles or attempts, or raise the limit")
	}
	return nil
}

func (s *testRehearsalSession) validateRevision(o testRehearsalOptions, manifestDir string) error {
	root := manifestDir
	if s.manifestDir != "." {
		for range strings.Split(s.manifestDir, string(filepath.Separator)) {
			root = filepath.Dir(root)
		}
	}
	for _, source := range append([]string{s.scenario.Source}, rehearsalServiceSources(s.scenario)...) {
		if filepath.IsAbs(source) {
			return errors.New("rehearsal source paths must be relative")
		}
		// Resolve each source against the extracted repository, never against the
		// invoking checkout or an absolute path supplied by either revision.
		if _, err := rehearsalRelativePath(root, filepath.Join(manifestDir, source)); err != nil {
			return err
		}
	}
	if o.Engine == "real-vm" {
		_, err := collectTestValidation(map[string]testScenario{o.Scenario: s.scenario}, manifestDir, o.Scenario)
		return err
	}
	source, err := resolveDeploySourceDir(manifestDir, s.scenario.Source)
	if err != nil {
		return err
	}
	_, _, err = testBehaviorIdentities(source, s.scenario.Behavior)
	return err
}

func rehearsalServiceSources(scenario testScenario) []string {
	var sources []string
	for _, service := range scenario.Services {
		if service.Source != "" {
			sources = append(sources, service.Source)
		}
	}
	return sources
}

func (s *testRehearsalSession) materialize(side int, dir string) (string, string, error) {
	root, err := extractDeployArchive(s.archives[side], dir)
	if err != nil {
		return "", "", err
	}
	for _, file := range s.files {
		if err := copyTestRehearsalFile(filepath.Join(s.trusted, filepath.FromSlash(file.Path)), filepath.Join(root, filepath.FromSlash(file.Path)), testBehaviorFileLimit); err != nil {
			return "", "", err
		}
	}
	_, digest, err := hashTestRehearsalTree(root)
	return filepath.Join(root, s.manifestDir), digest, err
}

func rehearsalRelativePath(root, path string) (string, error) {
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return "", err
	}
	rel, err := filepath.Rel(root, resolved)
	if err != nil || !filepath.IsLocal(rel) {
		return "", errors.New("rehearsal inputs and sources must stay inside the Git checkout")
	}
	return rel, nil
}

func copyTestRehearsalFile(source, destination string, limit int64) error {
	in, err := openCustomerFile(source)
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()
	info, err := in.Stat()
	if err != nil {
		return err
	}
	if info.Size() > limit {
		return errors.New("trusted input exceeds the declared behavior file size limit")
	}
	if err := os.MkdirAll(filepath.Dir(destination), 0o700); err != nil {
		return err
	}
	out, err := os.OpenFile(destination, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600|info.Mode()&0o111)
	if err != nil {
		return err
	}
	n, copyErr := io.Copy(out, io.LimitReader(in, limit+1))
	modeErr := out.Chmod(0o600 | info.Mode()&0o111)
	closeErr := out.Close()
	if n > limit {
		return errors.New("trusted input exceeds the declared behavior file size limit")
	}
	return errors.Join(copyErr, modeErr, closeErr)
}

func hashTestRehearsalTree(root string) ([]testBehaviorFile, string, error) {
	var files []testBehaviorFile
	var total int64
	digest := sha256.New()
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil || entry.IsDir() {
			return walkErr
		}
		if !entry.Type().IsRegular() {
			return errors.New("rehearsal snapshots support only regular files and directories")
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		total += info.Size()
		if len(files) >= api.SourceArchiveMaxEntries || total > deployArchiveMaxExpandedBytes {
			return errors.New("rehearsal snapshot exceeds the source archive envelope")
		}
		rel, _ := filepath.Rel(root, path)
		fileDigest, err := tarballSHA256(path)
		if err != nil {
			return err
		}
		files = append(files, testBehaviorFile{Path: filepath.ToSlash(rel), SHA256: fileDigest})
		_, _ = fmt.Fprintf(digest, "%s\x00%o\x00%s\x00", filepath.ToSlash(rel), info.Mode()&0o111, fileDigest)
		return nil
	})
	sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })
	return files, hex.EncodeToString(digest.Sum(nil)), err
}

func resolveTestRehearsalRevision(ctx context.Context, repo, ref string) (testRehearsalRevision, error) {
	if ref == "" || strings.HasPrefix(ref, "-") || strings.ContainsAny(ref, "\x00\r\n") {
		return testRehearsalRevision{}, errors.New("revision must be a local Git commit reference")
	}
	commit, err := rehearsalGitOutput(ctx, repo, "rev-parse", "--verify", "--end-of-options", ref+"^{commit}")
	if err != nil {
		return testRehearsalRevision{}, err
	}
	commit = strings.TrimSpace(commit)
	if decoded, err := hex.DecodeString(commit); err != nil || (len(decoded) != 20 && len(decoded) != 32) {
		return testRehearsalRevision{}, errors.New("git returned an invalid commit identity")
	}
	tree, err := rehearsalGitOutput(ctx, repo, "rev-parse", "--verify", commit+"^{tree}")
	return testRehearsalRevision{Ref: ref, Commit: commit, Tree: strings.TrimSpace(tree)}, err
}

func rehearsalGitCommand(ctx context.Context, repo string, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, "git", append([]string{"--no-replace-objects", "-C", repo}, args...)...)
	for _, variable := range os.Environ() {
		name, _, _ := strings.Cut(variable, "=")
		if !strings.HasPrefix(name, "GIT_") {
			cmd.Env = append(cmd.Env, variable)
		}
	}
	cmd.Env = append(cmd.Env, "GIT_TERMINAL_PROMPT=0", "GIT_NO_LAZY_FETCH=1")
	cmd.WaitDelay = time.Second
	return cmd
}

func rehearsalGitOutput(ctx context.Context, repo string, args ...string) (string, error) {
	cmd := rehearsalGitCommand(ctx, repo, args...)
	var output bytes.Buffer
	cmd.Stdout = &testRehearsalLimitWriter{writer: &output, remaining: 1 << 20}
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("local Git %s failed: %w", args[0], err)
	}
	return output.String(), nil
}

func archiveTestRehearsalRevision(ctx context.Context, repo, commit, destination string) error {
	// git archive omits gitlink contents. Refuse them explicitly rather than
	// claiming that an incomplete checkout represents the pinned revision.
	modes, err := rehearsalGitOutput(ctx, repo, "ls-tree", "-r", "--format=%(objectmode)", commit)
	if err != nil {
		return err
	}
	if strings.Contains(modes, "160000") {
		return errors.New("rehearsals do not yet materialize Git submodules")
	}
	file, err := os.OpenFile(destination, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	compressed := gzip.NewWriter(&testRehearsalLimitWriter{writer: file, remaining: deployArchiveMaxCompressedBytes})
	cmd := rehearsalGitCommand(ctx, repo, "archive", "--format=tar", "--prefix=source/", commit)
	cmd.Stdout = &testRehearsalLimitWriter{writer: compressed, remaining: deployArchiveMaxExpandedBytes}
	runErr := cmd.Run()
	return errors.Join(runErr, compressed.Close(), file.Close())
}

type testRehearsalLimitWriter struct {
	writer    io.Writer
	remaining int64
}

func (w *testRehearsalLimitWriter) Write(p []byte) (int, error) {
	if int64(len(p)) > w.remaining {
		return 0, errors.New("git snapshot exceeds the source envelope")
	}
	n, err := w.writer.Write(p)
	w.remaining -= int64(n)
	return n, err
}
