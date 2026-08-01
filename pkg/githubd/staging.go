// staging.go — per-app source staging for the githubd push-dispatch
// path (issue #432 phase 5).
//
// The githubd dispatcher fetches the full repo tree via Source.Fetch
// (codeload archive for the bound installation). After the reconcile
// step fans out the touched apps, each app's RootDir subtree needs
// to land on disk as a per-app .tar.gz so the apid bridge can hand
// the path to apid's CreateDeployment, which stamps it on the
// deployment row's SourcePath. builderd reads SourcePath as a local
// file (pkg/builderd/builderd.go:321) — no URL fetch path.
//
// This file owns:
//
//   - stageAppSource: Service method that stages one app's
//     RootDir subtree into <WorkDir>/build-sources/<account>/
//     <app>/<commit_sha>/source.tar.gz.
//   - repackageRootTree: the per-app gzip-tar walk.
//
// The staging path is stable for the daemon's lifetime (keyed
// on commit SHA), so a re-push of the same SHA overwrites the
// tarball in place. The gitfetch temp dir is consumed by the
// deferred tree.Close() AFTER staging (the staging step happens
// in the dispatch loop while tree.FS() is still live).

package githubd

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"

	"github.com/onebox-faas/faas/pkg/state"
)

// stageAppSource stages the per-app RootDir subtree into the
// githubd workdir and returns:
//
//   - sourcePath: the absolute path to the staged .tar.gz on disk
//   - sourceBytes: the on-disk size of the tarball
//   - sourceURL: the upstream archive URL (codeload) for provenance
//   - err: non-nil on staging failure (the caller logs + skips)
//
// The codeload URL is the only URL githubd actually fetches;
// builderd never reads it (pkg/builderd/builderd.go:321 reads
// SourcePath as a local file). The URL is the provenance-only
// source_url the apid bridge stamps on the deployment row.
//
// WorkDir empty is a test-only condition: the production wiring
// in cmd/githubd/main.go sets WorkDir to githubdWorkDir() before
// the dispatcher runs. Empty WorkDir returns an error so the
// test that forgot to wire it surfaces a clear failure (instead
// of staging into the wrong directory).
func (s *Service) stageAppSource(ctx context.Context, tree SourceTree, app state.App, project state.Project, commitSHA, branch string) (sourcePath string, sourceBytes int64, sourceURL string, err error) {
	if s.WorkDir == "" {
		return "", 0, "", errors.New("githubd: Service.WorkDir is empty (production wiring sets it to FAAS_GITHUBD_WORK_DIR)")
	}
	if tree == nil {
		return "", 0, "", errors.New("githubd: stageAppSource: nil tree (caller bug)")
	}
	// The staging directory is keyed on (account_id, app_id,
	// commit_sha) so a re-push of the same SHA overwrites the
	// tarball in place. The slash-separated layout mirrors
	// cmd/apid/deploy_inputs.go:192-195 (per-deployment dir
	// under the spool root).
	stagingDir := filepath.Join(s.WorkDir, "build-sources", project.AccountID, app.ID, commitSHA)
	if mkErr := os.MkdirAll(stagingDir, 0o750); mkErr != nil {
		return "", 0, "", fmt.Errorf("githubd: create staging dir %q: %w", stagingDir, mkErr)
	}
	dstTarball := filepath.Join(stagingDir, "source.tar.gz")

	// Honor ctx cancellation by short-circuiting the walk. The
	// repackageRootTree walker checks ctx between files (cheap;
	// inotify-style). A cancelled ctx returns a partial tarball
	// — the caller logs + skips, the next re-push re-stages.
	if err := repackageRootTree(ctx, tree.FS(), app.RootDir, dstTarball); err != nil {
		// Best-effort: don't leave a half-written tarball on
		// disk (a future EnqueueBuild would pick it up via
		// the (account, app, sha) cache key and read a
		// truncated file).
		_ = os.Remove(dstTarball)
		return "", 0, "", fmt.Errorf("githubd: repackage root tree: %w", err)
	}
	// Stat the tarball for the size the apid bridge will
	// stamp on the deployment row's SourceBytes.
	st, statErr := os.Stat(dstTarball)
	if statErr != nil {
		return "", 0, "", fmt.Errorf("githubd: stat staged tarball %q: %w", dstTarball, statErr)
	}
	// Build the provenance URL. The codeload archive URL is
	// the upstream githubd fetched; builderd never reads it,
	// but the §11 audit trail records it. The exact shape
	// matches what githubd's installationSourceFetcher
	// effectively downloads (cmd/githubd/source_fetcher.go);
	// the URL stays stable for a given (repo, ref).
	sourceURL = buildCodeloadURL("https://codeload.github.com", project.RepoFullName, branch, commitSHA)
	return dstTarball, st.Size(), sourceURL, nil
}

// repackageRootTree walks the fs.FS subtree under rootDir and
// writes a gzip-compressed tar archive to dstTarball. The
// output is byte-for-byte what builderd's gzip.NewReader
// (pkg/builderd/detect.go:48) expects to read.
//
// The walk is depth-first; ordering is sorted by path so the
// output is deterministic — a re-stage of the same tree
// produces the same tarball bytes (modulo file mtimes that
// fs.FS typically doesn't expose; the apid bridge's
// SourceBytes check is on size, not content hash).
//
// Empty rootDir ("") walks the repo root — that's the
// "single-app project" case where the whole repo IS the app's
// source. RootDir "/foo" walks everything under that prefix
// (the tar entry names are rebased to "/" so the resulting
// tarball is rooted at the app, not the repo).
//
// ctx cancellation is honored between files (not within a
// single file — io.Copy would block on a slow read). The
// walker checks ctx.Err() at the top of each iteration.
func repackageRootTree(ctx context.Context, src fs.FS, rootDir, dstTarball string) error {
	dst, err := os.Create(dstTarball) //nolint:gosec // dst is operator-controlled
	if err != nil {
		return fmt.Errorf("create tarball: %w", err)
	}
	// Track close-on-error so a partial write doesn't leak
	// the file descriptor.
	committed := false
	defer func() {
		if !committed {
			_ = dst.Close()
		}
	}()
	gz := gzip.NewWriter(dst)
	tw := tar.NewWriter(gz)

	// fs.WalkDir fails with "open : file does not exist"
	// when the root is "" — Go's fs.WalkDir calls
	// src.Open("") on the empty string and the standard
	// fs.FS implementations reject it. "." is the
	// canonical "root of this FS" path and is the same
	// shape the standard library uses internally.
	walkRoot := rootDir
	if walkRoot == "" {
		walkRoot = "."
	}

	walkErr := fs.WalkDir(src, walkRoot, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			// A missing entry under rootDir is a
			// githubd-side bug (the reconcile step
			// produced a RootDir that doesn't exist in
			// the tree). Surface it as a hard error.
			return err
		}
		// Honor ctx cancellation between entries.
		if cerr := ctx.Err(); cerr != nil {
			return cerr
		}
		// Skip the walk root itself (it's the entry we
		// walked into; emitting it as a directory entry
		// would add a leading "/" to the tarball that
		// builderd doesn't expect).
		if p == walkRoot {
			return nil
		}
		// Rebase the path so the tarball is rooted at
		// the app (not the repo). path.Rel handles
		// both the "" rootDir case (yields p verbatim)
		// and the "/foo" rootDir case (yields
		// "everything-under-foo"). For the "" rootDir
		// case we pass "." to filepath.Rel so a file
		// at walkRoot/. returns the bare file name.
		relBase := rootDir
		if relBase == "" {
			relBase = "."
		}
		rel, relErr := filepath.Rel(relBase, p)
		if relErr != nil {
			return relErr
		}
		// Normalize to forward slashes for tar
		// (filepath.Rel on Unix yields forward
		// slashes already; this is belt-and-suspenders
		// for future cross-platform support).
		rel = path.Clean(filepath.ToSlash(rel))
		if rel == "." {
			return nil
		}
		info, infoErr := d.Info()
		if infoErr != nil {
			return infoErr
		}
		hdr, herr := tar.FileInfoHeader(info, "")
		if herr != nil {
			return herr
		}
		hdr.Name = rel
		if err := tw.WriteHeader(hdr); err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		// Regular file — copy the body. fs.FS.Open
		// returns a fresh handle each call, so we
		// can defer Close inside the loop body.
		f, ferr := src.Open(p)
		if ferr != nil {
			return ferr
		}
		if _, cerr := io.Copy(tw, f); cerr != nil {
			_ = f.Close()
			return cerr
		}
		if cerr := f.Close(); cerr != nil {
			return cerr
		}
		return nil
	})
	if walkErr != nil {
		_ = tw.Close()
		_ = gz.Close()
		return walkErr
	}
	if err := tw.Close(); err != nil {
		_ = gz.Close()
		return err
	}
	if err := gz.Close(); err != nil {
		return err
	}
	if err := dst.Close(); err != nil {
		return err
	}
	committed = true
	return nil
}

// buildCodeloadURL constructs the upstream archive URL for
// provenance. The format mirrors GitHub's codeload endpoint:
//
//	https://codeload.github.com/{owner}/{repo}/tar.gz/{ref}
//
// The URL is provenance-only (builderd reads SourcePath, not
// SourceURL). Kept as a free function so a future gitfetch
// variant can swap the host (e.g. a self-hosted mirror).
func buildCodeloadURL(host, repoFullName, ref, commitSHA string) string {
	// Prefer the commit SHA when available — the SHA
	// reference is stable across force-pushes; the branch
	// reference can move. githubd already has the SHA from
	// the push event (ev.After).
	if commitSHA != "" {
		return fmt.Sprintf("%s/%s/tar.gz/%s", host, repoFullName, commitSHA)
	}
	return fmt.Sprintf("%s/%s/tar.gz/%s", host, repoFullName, ref)
}
