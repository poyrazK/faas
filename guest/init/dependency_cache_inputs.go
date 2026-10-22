package main

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"hash"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/onebox-faas/faas/pkg/devpatch"
)

// The app-scoped BuildKit dependency cache is exported with mode=max, which
// rewrites every layer, base image layers included, on every build: on a
// slow builder disk that took longer than the build itself. A Railpack build
// whose plan and install inputs match the ones that produced the imported
// cache installs exactly the same layers, so it keeps that cache instead of
// exporting it again. BuildKit still validates every imported layer, so a
// wrong decision here can only cost a cache miss, never a wrong image.
const (
	// dependencyInputsHelperEnv selects the helper in a re-executed
	// guest-init; the build shell runs it between `railpack prepare` and
	// buildctl, when the plan exists.
	dependencyInputsHelperEnv   = "GREGALE_INIT_HELPER"
	dependencyInputsHelperValue = "dependency-cache-inputs"
	// dependencyInputsFile holds the digest beside index.json in a stored
	// cache, and in the build output until the cache is exported.
	dependencyInputsFile = "gregale-dependency-inputs"
	// dependencyCacheUnchangedMarker in the build output tells writeBuildDone
	// that the imported cache was kept on purpose.
	dependencyCacheUnchangedMarker = "dependency-cache-unchanged"
	dependencyInputsMaxFiles       = 4096
)

// dependencyInputsDigest hashes what decides a Railpack build's dependency
// layers: the frontend identity, the plan, and every install input under
// workdir (devpatch.RailpackInstallInputs).
func dependencyInputsDigest(identity string, plan []byte, workdir string) (string, error) {
	inputs, err := devpatch.RailpackInstallInputs(plan)
	if err != nil {
		return "", err
	}
	h := sha256.New()
	writeField(h, identity)
	writeField(h, string(plan))
	files := 0
	for _, rel := range inputs {
		writeField(h, rel)
		if err := hashInputPath(h, workdir, rel, &files); err != nil {
			return "", err
		}
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func writeField(h hash.Hash, value string) {
	_, _ = fmt.Fprintf(h, "%d:%s\n", len(value), value)
}

// hashInputPath adds one install input: a file's bytes, a directory's files
// in lexical order, a link's target, or its absence.
func hashInputPath(h hash.Hash, workdir, rel string, files *int) error {
	root := filepath.Join(workdir, filepath.FromSlash(rel))
	if !strings.HasPrefix(root, filepath.Clean(workdir)+string(filepath.Separator)) {
		writeField(h, "outside")
		return nil
	}
	return filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if errors.Is(err, fs.ErrNotExist) && path == root {
			writeField(h, "absent")
			return nil
		}
		if err != nil {
			return err
		}
		name, _ := filepath.Rel(workdir, path)
		switch {
		case entry.IsDir():
			writeField(h, "dir "+filepath.ToSlash(name))
			return nil
		case entry.Type()&fs.ModeSymlink != 0:
			target, err := os.Readlink(path)
			if err != nil {
				return err
			}
			writeField(h, "link "+filepath.ToSlash(name)+" "+target)
			return nil
		case !entry.Type().IsRegular():
			writeField(h, "special "+filepath.ToSlash(name))
			return nil
		}
		if *files++; *files > dependencyInputsMaxFiles {
			return fmt.Errorf("more than %d install input files", dependencyInputsMaxFiles)
		}
		writeField(h, "file "+filepath.ToSlash(name))
		//nolint:forbidigo // the builder VM reads its own build context; no host daemon is involved.
		f, err := os.Open(path)
		if err != nil {
			return err
		}
		defer func() { _ = f.Close() }()
		_, err = io.Copy(h, f)
		return err
	})
}

// runDependencyCacheInputs is the helper's body. It prints "keep" when the
// imported cache was produced from the same inputs, else "export", and
// records the digest in outDir for the cache about to be exported. Any
// failure answers "export": the old behaviour.
func runDependencyCacheInputs(identity string, args []string, stdout io.Writer) int {
	if len(args) != 4 {
		_, _ = fmt.Fprintln(stdout, "export")
		return 0
	}
	planPath, workdir, importedDir, outDir := args[0], args[1], args[2], args[3]
	plan, err := os.ReadFile(planPath)
	if err != nil {
		_, _ = fmt.Fprintln(stdout, "export")
		return 0
	}
	digest, err := dependencyInputsDigest(identity, plan, workdir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "guest-init: dependency inputs not hashed, exporting the cache: %v\n", err)
		_, _ = fmt.Fprintln(stdout, "export")
		return 0
	}
	if previous, err := os.ReadFile(filepath.Join(importedDir, dependencyInputsFile)); err == nil && strings.TrimSpace(string(previous)) == digest {
		if err := os.WriteFile(filepath.Join(outDir, dependencyCacheUnchangedMarker), []byte(digest+"\n"), 0o600); err == nil {
			fmt.Fprintln(os.Stderr, "guest-init: dependency inputs unchanged; keeping the imported dependency cache")
			_, _ = fmt.Fprintln(stdout, "keep")
			return 0
		}
	}
	_ = os.WriteFile(filepath.Join(outDir, dependencyInputsFile), []byte(digest+"\n"), 0o600)
	_, _ = fmt.Fprintln(stdout, "export")
	return 0
}

// finishDependencyCacheInputs moves the digest into an exported cache, so
// the next build can compare against it.
func finishDependencyCacheInputs(outDir string) {
	digest := filepath.Join(outDir, dependencyInputsFile)
	cacheIndex := filepath.Join(outDir, "cache", "index.json")
	if _, err := os.Stat(cacheIndex); err != nil {
		_ = os.Remove(digest)
		return
	}
	if err := os.Rename(digest, filepath.Join(outDir, "cache", dependencyInputsFile)); err != nil && !errors.Is(err, fs.ErrNotExist) {
		fmt.Fprintf(os.Stderr, "guest-init: dependency inputs not recorded: %v\n", err)
	}
}

// dependencyCacheKept reports whether this build kept the imported cache.
func dependencyCacheKept(outDir string) bool {
	_, err := os.Stat(filepath.Join(outDir, dependencyCacheUnchangedMarker))
	return err == nil
}
