package main

import (
	"archive/tar"
	"bufio"
	"bytes"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidsource"
	"github.com/onebox-faas/faas/pkg/middleware"
	"github.com/onebox-faas/faas/pkg/sourcecontext"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/tarball"
)

// Source tarball + dockerfile + function deploys (spec §9).
//
// apid is the *Accept* step (spec §9 step 1). It validates the tarball
// shape, spools it to disk, creates the queued build row, and notifies
// builderd via pg_notify('build_queued'). builderd (M6) is the actual
// executor; in M5 the build row just sits in 'queued' state.

// maxSourceFiles caps tarball entries at 10k (spec §9). The source of truth
// lives with the platform limits because developer delta reconstruction must
// enforce the identical archive shape.
const maxSourceFiles = api.SourceArchiveMaxEntries

// sourceSpoolRoot is where apid drops source tarballs before imaged /
// builderd process them. The dir is host-configurable via env to keep tests
// hermetic.
const sourceSpoolRootEnv = "FAAS_SPOOL_ROOT"

func marshalWorkflowDefinitions(definitions []api.WorkflowSpec) json.RawMessage {
	if len(definitions) == 0 {
		return nil
	}
	raw, err := json.Marshal(definitions)
	if err != nil {
		return nil
	}
	return raw
}

func spoolRoot() string {
	if v := os.Getenv(sourceSpoolRootEnv); v != "" {
		return v
	}
	return "/var/spool/faas/builds"
}

// createDeploymentMultipart handles source tarball + dockerfile + function
// source uploads (spec §4.2, §9). Multipart form fields:
//
//	source    file   — tarball (tar.gz). Required when no image field is set.
//	image     string — alternative to source; a registry digest reference.
//	dockerfile bool  — present if the tarball root contains a Dockerfile.
//	runtime   string — node22|python312 for function deploys.
//	handler   string — handler path, required when runtime is set.
//
// DeployedApps is enforced at app-create time via
// store.CreateAppIfUnderQuota — the deploy path cannot bypass it because
// the parent apps row must already exist. The active-app gate that
// prevents an orphan deployment row pointing at a soft-deleted app
// lives inside store.CreateDeployment (PR-A: SELECT 1 FROM apps
// WHERE id=$1 AND status='active' FOR UPDATE).
func (s *server) createDeploymentMultipart(w http.ResponseWriter, r *http.Request, acct state.Account, app state.App, developerSource bool) {
	limits := api.MustLimitsFor(acct.Plan)

	// The body has already been wrapped in http.MaxBytesReader at the
	// dispatch site (handlers.go:createDeployment) so r.MultipartReader()
	// will surface a *http.MaxBytesError as a parse error if the upload
	// exceeds the plan's SourceTarballMaxMB. No pre-Check here.

	mr, err := r.MultipartReader()
	if err != nil {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation,
			"Bad multipart", err.Error()))
		return
	}

	var (
		sourcePath     string
		sourceBytes    int64
		dockerfile     bool
		runtime        string
		handler        string
		sourceRoot     string
		kind           state.DeploymentKind
		sourceAccepted bool
		workflows      []api.WorkflowSpec
		devSource      devSourceMetadata
	)
	defer func() {
		if sourcePath != "" && !sourceAccepted {
			_ = os.Remove(sourcePath)
		}
	}()

	for {
		part, err := mr.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation,
				"Bad multipart", err.Error()))
			return
		}
		name := part.FormName()
		switch name {
		case "source":
			kind = state.DeploymentKindTarball
			if vErr := assertMultipartFileName(part); vErr != nil {
				api.WriteProblem(w, vErr)
				return
			}
			path, n, vErr := validateAndSpool(part, limits)
			if vErr != nil {
				api.WriteProblem(w, vErr)
				return
			}
			if sourcePath != "" {
				_ = os.Remove(sourcePath)
			}
			sourcePath, sourceBytes = path, n
		case "dockerfile":
			dockerfile = isFlagSet(part)
			if dockerfile {
				kind = state.DeploymentKindDockerfile
			}
		case "runtime":
			b, _ := io.ReadAll(io.LimitReader(part, 64))
			runtime = strings.TrimSpace(string(b))
		case "handler":
			b, _ := io.ReadAll(io.LimitReader(part, 256))
			handler = strings.TrimSpace(string(b))
		case "source_root":
			b, readErr := io.ReadAll(io.LimitReader(part, sourcecontext.MaxRootBytes+1))
			if readErr != nil || len(b) > sourcecontext.MaxRootBytes {
				api.WriteProblem(w, api.ErrSourceInvalid("source_root is too long"))
				return
			}
			storedRoot, rootErr := sourcecontext.StorageRoot(string(b))
			if rootErr != nil {
				api.WriteProblem(w, api.ErrSourceInvalid("invalid source_root: "+rootErr.Error()))
				return
			}
			sourceRoot = storedRoot
		case "workflows":
			b, readErr := io.ReadAll(io.LimitReader(part, 1<<20))
			if readErr != nil || !json.Valid(b) {
				api.WriteProblem(w, api.ErrValidation("workflows must be valid JSON"))
				return
			}
			if err := json.Unmarshal(b, &workflows); err != nil {
				api.WriteProblem(w, api.ErrValidation("workflows must be a JSON array of definitions"))
				return
			}
			if prob := validateWorkflowDefinitionsAgainstPlan(workflows, acct.Plan); prob != nil {
				api.WriteProblem(w, prob)
				return
			}
		case "dev_source_base", "dev_source_target", "dev_source_deleted":
			if !developerSource {
				_, _ = io.Copy(io.Discard, part)
				break
			}
			if prob := devSource.readField(name, part); prob != nil {
				api.WriteProblem(w, prob)
				return
			}
		default:
			// Ignore unknown fields so clients can ship extra metadata.
			_, _ = io.Copy(io.Discard, part)
		}
		_ = part.Close()
	}

	if sourcePath == "" {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation,
			"Source required", "multipart deploys require a 'source' file field"))
		return
	}
	if developerSource {
		preparedPath, preparedBytes, prob := s.prepareDevSource(acct, app, sourcePath, devSource, limits)
		if prob != nil {
			api.WriteProblem(w, prob)
			return
		}
		if preparedPath != sourcePath {
			_ = os.Remove(sourcePath)
			sourcePath = preparedPath
		}
		sourceBytes = preparedBytes
	}
	if sourceRoot != "" {
		present, rootErr := archiveHasSourceRoot(sourcePath, sourceRoot)
		if rootErr != nil {
			api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeSourceInvalid, "Bad source", rootErr.Error()))
			return
		}
		if !present {
			api.WriteProblem(w, api.ErrSourceInvalid(fmt.Sprintf("source_root %q is not present in the source archive", sourceRoot)))
			return
		}
	}

	// Wave 0 / year-one stateless-only: detect persistence-shaped
	// deploys at accept time so we fail fast (no build slot wasted).
	// Two checks run inside one tarball pass:
	//   - Dockerfile scan: VOLUME instruction, mkfs/mount -t ext4|xfs
	//     inside a RUN. Only when the customer marked dockerfile=true
	//     OR a Dockerfile exists at the selected source root (Railpack detect
	//     handles the latter case, but we surface the violation here).
	//   - Source root: a `data/` or `db/` directory at the selected root
	//     is the canonical "this is a database" signal.
	// Both pass the same spooled tarball — no extra I/O.
	if prob := scanForStatefulShapeAtRoot(sourcePath, dockerfile, sourceRoot); prob != nil {
		api.WriteProblem(w, prob)
		return
	}
	if prob := scanSourceTarballSecrets(sourcePath, limits); prob != nil {
		api.WriteProblem(w, prob)
		return
	}
	hasRootDockerfile, err := archiveHasRootDockerfileAtRoot(sourcePath, sourceRoot)
	if err != nil {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeSourceInvalid, "Bad source", err.Error()))
		return
	}

	// Function rewrites (spec §4.2): source + runtime + handler becomes a
	// function deploy using the runner scaffold (§4.9). The runtime must
	// be present and the handler must point at a real export.
	if app.Type == state.AppTypeFunction {
		if dockerfile || hasRootDockerfile {
			api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation,
				"Dockerfile functions unsupported",
				"function deploys use the platform runtime scaffold; remove the Dockerfile and dockerfile=true"))
			return
		}
		kind = state.DeploymentKindTarball
		if runtime != "" && runtime != app.Runtime {
			api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation,
				"Runtime mismatch", "function deploys must match the app's runtime"))
			return
		}
		if handler == "" {
			api.WriteProblem(w, api.ErrHandlerMissing())
			return
		}
	}

	if sourcePath != "" {
		// PR-B: the prior-deployment supersede is folded into
		// store.CreateDeployment's tx (pkg/state/pgstore.go). The tarball
		// branch picks up the parity the image: branch used to lack —
		// every successful source deploy now atomically supersedes the
		// prior non-terminal row. The second NotifyDeploymentChanged
		// for `prev` (fired by apidsource.Enqueue below) lets imaged
		// F5-cleanup the prior snapshot. No call-site change was needed
		// to gain this; the in-tx ordering is invisible above the Store
		// seam.
		//
		// The wire "source" payload value is now derived from Kind
		// inside apidsource.Enqueue (H1 fix from PR #541 review) — the
		// legacy hardcoded "image" diverged from Kind for tarball /
		// dockerfile deploys and produced misleading split-by-source
		// dashboards.
		_, err := apidsource.Enqueue(r.Context(), s.store, s.notif, apidsource.EnqueueParams{
			AppID:       app.ID,
			Kind:        kind,
			SourcePath:  sourcePath,
			SourceBytes: sourceBytes,
			SourceRoot:  sourceRoot,
			Handler:     handler,
			LogSpool:    spoolRoot(),
			Log:         s.log,
			ActorUserID: acct.ID,
			ActorVia:    routeKindForRequest(r),
			ActorFromIP: middleware.ClientIP(r),
			Workflows:   marshalWorkflowDefinitions(workflows),
		})
		if err != nil {
			api.WriteProblem(w, api.ErrCapacity("could not create deployment"))
			return
		}
		if developerSource {
			if err := s.publishDevSource(acct, app, sourcePath, devSource.target, limits); err != nil {
				s.log.Warn("developer source cache publish failed", "app_id", app.ID, "error", err)
			}
		}
		sourceAccepted = true
		// Look up the durable deployment row to build the wire
		// response. LatestDeployment returns the row we just
		// inserted (state.Store.CreateDeployment is its own tx
		// and the row is committed before Enqueue returns).
		d, err := s.store.LatestDeployment(r.Context(), app.ID)
		if err != nil {
			api.WriteProblem(w, api.ErrCapacity("could not read deployment"))
			return
		}
		writeJSON(w, http.StatusAccepted, s.deploymentResponse(d, app))
		return
	}
}

// validateAndSpool reads a tar.gz from r, validates its shape, and
// writes it to the spool dir. Returns (spool_path, bytes, problem).
//
// Order is: write to a tmp path, validate, then atomically rename to the
// canonical path. This avoids leaving a malformed or oversized tarball at the
// canonical spool path where builderd could race to pick it up before the
// validation result is known.
//
// The multipart and source-ref (DEPLOY-PROV-4 / ADR-092, issue #739)
// deploy paths share this seam: the multipart caller wraps the
// *multipart.Part in a thin assertFileName helper, the source-ref
// caller passes the githubd-streamed io.ReadCloser straight through.
// This is the canonical io.Reader seam — the multipart.Part already
// implements Read so no adapter is needed.
//
// The caller MUST enforce SourceTarballMaxMB at the io.LimitReader
// layer (PR-A invariant — handleSourceRefDeploy wraps the streaming
// reader before this function sees it, so this seam receives a body
// that's already cap-bound at the wire level). The redundant
// post-Copy cap check below stays as a defence-in-depth: a future
// caller that forgets the LimitReader wrapper should still trip a
// CodeSourceTooLarge before spooling.
func validateAndSpool(r io.Reader, limits api.Limits) (string, int64, *api.Problem) {
	// The split-box builderd worker materializes the same archive into this
	// spool path after fetching it from shared storage. Keep the directory
	// group-writable so apid (faas-apid) and builderd (faas-builderd) can
	// cooperate through the faas group without a host-specific rsync user.
	if err := os.MkdirAll(spoolRoot(), 0o770); err != nil {
		return "", 0, api.ErrCapacity("could not create spool dir")
	}
	if err := os.Chmod(spoolRoot(), 0o770); err != nil {
		return "", 0, api.ErrCapacity("could not prepare spool dir")
	}
	id := randomToken(12)
	dst := filepath.Join(spoolRoot(), id+".tar.gz")
	tmp := dst + ".tmp"
	out, err := os.Create(tmp)
	if err != nil {
		return "", 0, api.ErrCapacity("could not spool source")
	}
	defer func() { _ = out.Close() }()

	n, err := io.Copy(out, r)
	if err != nil {
		_ = os.Remove(tmp)
		return "", 0, api.NewProblem(http.StatusBadRequest, api.CodeValidation,
			"Bad source", err.Error())
	}

	if n > int64(limits.SourceTarballMaxMB)*1024*1024 {
		_ = os.Remove(tmp)
		return "", 0, api.ErrSourceTooLarge(limits, n)
	}

	if prob := validateTarballShape(tmp); prob != nil {
		_ = os.Remove(tmp)
		return "", 0, prob
	}
	if err := os.Rename(tmp, dst); err != nil {
		_ = os.Remove(tmp)
		return "", 0, api.ErrCapacity("could not finalize spool")
	}
	return dst, n, nil
}

// assertMultipartFileName is the multipart-specific guard the
// validateAndSpool io.Reader seam absorbs. The source-ref path
// streams from githubd, so FileName is meaningless there; the
// guard exists only to keep the legacy multipart check
// (cmd/apid/deploy_inputs.go had a 400 here pre-PR-A) untouched.
func assertMultipartFileName(part *multipart.Part) *api.Problem {
	if part.FileName() == "" {
		return api.NewProblem(http.StatusBadRequest, api.CodeValidation,
			"Bad source", "source field must be a file")
	}
	return nil
}

// validateTarballShape opens the spooled tarball and verifies the §9
// invariants: ≤10k files, no symlink/hardlink escapes, no absolute
// paths, no `..` entries.
//
//nolint:forbidigo // path is the tmp file apid just wrote via os.Create in validateAndSpool above with a fresh random id; apid OWNS the parent directory AND the inode, customer never touched them — symlink-attack impossible. Tarball-shape validation re-reads the bytes to enforce spec §9.
func validateTarballShape(path string) *api.Problem {
	f, err := os.Open(path)
	if err != nil {
		return api.NewProblem(http.StatusBadRequest, api.CodeSourceInvalid, "Bad source", err.Error())
	}
	defer func() { _ = f.Close() }()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return api.NewProblem(http.StatusBadRequest, api.CodeSourceInvalid, "Not gzip", "source must be tar.gz")
	}
	defer func() { _ = gz.Close() }()
	tr := tar.NewReader(gz)
	count := 0
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return api.NewProblem(http.StatusBadRequest, api.CodeSourceInvalid, "Bad tar", err.Error())
		}
		// PR-A: every name-based escape check runs BEFORE count++ so a
		// tarball mixing 10k valid entries with one escaping symlink
		// trips the escape check first, not the file-count cap
		// (review ordering pin).
		if escapesArchiveRoot(hdr.Name) {
			return api.ErrSourceInvalid("absolute paths or '..' entries are rejected")
		}
		if hdr.Typeflag == tar.TypeSymlink || hdr.Typeflag == tar.TypeLink {
			// Symlink/hardlink target uses the same predicate as the
			// entry name. tar's tar.Reader doesn't resolve targets —
			// builderd's unpack does — so we just reject anything that
			// could escape when resolved relative to the entry's parent.
			if escapesArchiveRoot(hdr.Linkname) {
				return api.ErrSourceInvalid("symlink/hardlink with absolute or '..' target rejected")
			}
		}
		count++
		if count > maxSourceFiles {
			return api.ErrSourceInvalid(fmt.Sprintf("too many files (>%d)", maxSourceFiles))
		}
	}
	return nil
}

// escapesArchiveRoot reports whether p would, when cleaned and joined
// under an archive root, walk above that root. Catches absolute paths
// and any path component equal to "..". PR-A review: the prior
// strings.Contains("..") rejected safe names like "file..txt" —
// splitting on the path separator and checking each component is the
// tightest predicate that still closes the escape.
func escapesArchiveRoot(p string) bool {
	if p == "" {
		return false
	}
	if strings.HasPrefix(p, "/") {
		return true
	}
	// filepath.SplitList won't help; split manually so we don't pull in
	// OS semantics (tar paths are always forward-slash on the wire).
	for _, part := range strings.Split(p, "/") {
		if part == ".." {
			return true
		}
	}
	return false
}

// statefulTopLevelDirs is the set of top-level directory names that
// flag the deploy as stateful. Empty directories are fine — the tar
// entry exists either way, so a zero-byte `data/` is caught the same
// as a populated one. Both names are the canonical "I'm trying to
// persist" signal that bypasses Dockerfile detection entirely.
var statefulTopLevelDirs = map[string]string{} //nolint:gochecknoglobals // constant lookup table

func init() {
	statefulTopLevelDirs["data"] = "top-level data/ directory — this platform is stateless"
	statefulTopLevelDirs["db"] = "top-level db/ directory — this platform is stateless"
}

// scanForStatefulShape is the Wave 0 stateless-only accept-time check.
// Reads the spooled tarball once and rejects with CodeStatelessOnlyViolation
// when the selected source root has a persistent shape. Three checks, all in one pass:
//
//  1. If a Dockerfile exists at the selected source root (or dockerfile=true was
//     sent), reject any VOLUME instruction or mkfs/mount -t ext4|xfs call
//     inside a RUN directive. Bounded: we only read up to dockerfileMaxBytes
//     of the Dockerfile so a multi-MB heredoc can't pin apid.
//  2. Reject a source-root-level data/ or db/ directory — the canonical
//     "this is a database" signal that bypasses Dockerfile detection.
//     Short-circuits the scan: as soon as the offending entry is
//     observed, the loop returns without reading the rest of the
//     tarball (customer pays for one entry, not the whole archive).
//  3. The base-image deny-list runs in pkg/imaged, not here — apid only
//     sees a tarball or an image: ref; the image: branch is enforced
//     where the image is pulled (pkg/imaged/handler.go buildImageLayer).
//
//nolint:forbidigo // path is the tmp file apid just wrote via os.Create in validateAndSpool above with a fresh random id; apid OWNS the parent directory AND the inode, customer never touched them — symlink-attack impossible. Stateless-shape validation re-reads the same bytes validateTarballShape just walked.
func scanForStatefulShape(path string, dockerfileFlag bool) *api.Problem {
	return scanForStatefulShapeAtRoot(path, dockerfileFlag, "")
}

// scanForStatefulShapeAtRoot applies the stateless-only checks to the selected
// source root. Files in sibling workspace packages are build inputs, but they
// must not make a selected app look stateful merely because they contain a
// top-level data/ or db/ directory of their own.
func scanForStatefulShapeAtRoot(path string, dockerfileFlag bool, sourceRoot string) *api.Problem {
	logicalRoot, rootErr := archiveLogicalRoot(path, sourceRoot)
	if rootErr != nil {
		return api.NewProblem(http.StatusBadRequest, api.CodeSourceInvalid, "Bad source", rootErr.Error())
	}
	f, err := os.Open(path) //nolint:forbidigo // path is a server-owned spool file created by validateAndSpool under spoolRoot; customer input cannot replace it.
	if err != nil {
		return api.NewProblem(http.StatusBadRequest, api.CodeSourceInvalid, "Bad source", err.Error())
	}
	defer func() { _ = f.Close() }()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return api.NewProblem(http.StatusBadRequest, api.CodeSourceInvalid, "Not gzip", "source must be tar.gz")
	}
	defer func() { _ = gz.Close() }()
	tr := tar.NewReader(gz)

	var dockerfileBytes []byte
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return api.NewProblem(http.StatusBadRequest, api.CodeSourceInvalid, "Bad tar", err.Error())
		}
		rel, inRoot := archiveEntryRelativeToRoot(hdr.Name, logicalRoot)
		if !inRoot {
			continue
		}
		parts := strings.SplitN(rel, "/", 2)
		if len(parts) >= 1 {
			if reason, denied := statefulTopLevelDirs[parts[0]]; denied {
				// Short-circuit: we don't need to read the rest of
				// the tarball now that we've found the violation.
				return api.ErrStatelessOnlyViolation("tarball", reason)
			}
		}
		// Only read the Dockerfile at the selected source root. We do this
		// lazily — dockerfileMaxBytes caps the read so a hostile
		// heredoc can't pin apid.
		if rel == "Dockerfile" {
			dockerfileBytes, _ = io.ReadAll(io.LimitReader(tr, dockerfileMaxBytes))
		}
	}

	// Check 1: Dockerfile scan. If dockerfile=true but no Dockerfile
	// was found at the selected source root, fail fast at accept time rather
	// than punting to a build-time failure — the customer asked for a
	// Dockerfile deploy and we shouldn't have to start a build slot to
	// tell them they forgot to include one.
	if dockerfileFlag && len(dockerfileBytes) == 0 {
		return api.NewProblem(http.StatusBadRequest, api.CodeSourceInvalid,
			"Dockerfile missing",
			"`dockerfile=true` was set but no Dockerfile was found at the selected source root")
	}
	if len(dockerfileBytes) > 0 {
		if reason := scanDockerfileForStatefulShape(dockerfileBytes); reason != "" {
			return api.ErrStatelessOnlyViolation("dockerfile", reason)
		}
	}
	return nil
}

// archiveHasRootDockerfile reports whether a source archive contains the
// selected-root Dockerfile that Railpack would use for a custom-image build. It
// is deliberately separate from scanForStatefulShape: a clean Dockerfile is
// valid for a container deploy but incompatible with a function deploy, which
// must use the selected runtime's normalized handler scaffold.
//
//nolint:forbidigo // path is the spool file created and owned by apid.
func archiveHasRootDockerfile(path string) (bool, error) {
	return archiveHasRootDockerfileAtRoot(path, "")
}

// archiveHasRootDockerfileAtRoot reports whether the selected source root has
// a Dockerfile. sourceRoot is relative to the logical archive root, not to a
// transport wrapper directory.
func archiveHasRootDockerfileAtRoot(path, sourceRoot string) (bool, error) {
	logicalRoot, err := archiveLogicalRoot(path, sourceRoot)
	if err != nil {
		return false, err
	}
	f, err := os.Open(path) //nolint:forbidigo // path is a server-owned spool file created by validateAndSpool under spoolRoot; customer input cannot replace it.
	if err != nil {
		return false, err
	}
	defer func() { _ = f.Close() }()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return false, err
	}
	defer func() { _ = gz.Close() }()
	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			return false, nil
		}
		if err != nil {
			return false, err
		}
		rel, inRoot := archiveEntryRelativeToRoot(hdr.Name, logicalRoot)
		if inRoot && rel == "Dockerfile" {
			return true, nil
		}
	}
}

// archiveLogicalRoot resolves the optional historical single-directory
// wrapper used by the CLI. A non-empty source root is then addressed below
// that wrapper, while an empty source root retains the old archive-root
// behavior.
func archiveLogicalRoot(path, sourceRoot string) (string, error) {
	return tarball.ResolveSourceRoot(path, sourceRoot)
}

// archiveEntryRelativeToRoot returns the slash-separated path of an archive
// entry below logicalRoot. An empty logicalRoot means a flat archive root.
func archiveEntryRelativeToRoot(name, logicalRoot string) (string, bool) {
	name = strings.TrimPrefix(strings.TrimSuffix(name, "/"), "./")
	if name == "" {
		return "", false
	}
	if logicalRoot == "" {
		return name, true
	}
	prefix := strings.TrimSuffix(logicalRoot, "/") + "/"
	if !strings.HasPrefix(name, prefix) {
		return "", false
	}
	rel := strings.TrimPrefix(name, prefix)
	if rel == "" {
		return "", false
	}
	return rel, true
}

// archiveHasSourceRoot checks that a non-default source root names a
// directory represented by at least one archive entry. Tar does not require
// explicit directory headers, so a child file is the durable existence test.
func archiveHasSourceRoot(path, sourceRoot string) (bool, error) {
	logicalRoot, err := archiveLogicalRoot(path, sourceRoot)
	if err != nil {
		return false, err
	}
	if logicalRoot == "" {
		return false, nil
	}
	f, err := os.Open(path) //nolint:forbidigo // path is a server-owned spool file created by validateAndSpool under spoolRoot; customer input cannot replace it.
	if err != nil {
		return false, err
	}
	defer func() { _ = f.Close() }()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return false, err
	}
	defer func() { _ = gz.Close() }()
	tr := tar.NewReader(gz)
	prefix := strings.TrimSuffix(logicalRoot, "/") + "/"
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			return false, nil
		}
		if err != nil {
			return false, err
		}
		switch hdr.Typeflag {
		case tar.TypeXHeader, tar.TypeXGlobalHeader,
			tar.TypeGNULongName, tar.TypeGNULongLink:
			continue
		}
		name := strings.TrimPrefix(strings.TrimSuffix(hdr.Name, "/"), "./")
		if strings.HasPrefix(name, prefix) && strings.TrimPrefix(name, prefix) != "" {
			return true, nil
		}
	}
}

// dockerfileMaxBytes caps how much of a Dockerfile we'll read in-process.
// 256 KiB is generous for a real Dockerfile (typical: <4 KiB) and bounds
// the per-deploy apid CPU cost.
const dockerfileMaxBytes = 256 * 1024

// scanDockerfileForStatefulShape walks a Dockerfile's bytes looking for
// persistence-shaped directives. Returns the offending line/instruction
// for inclusion in the RFC 7807 body, or "" if clean.
//
// The checks are intentionally narrow: VOLUME on its own line, and
// mkfs/mount -t ext4|xfs inside any RUN. We do NOT try to be clever
// about RUN continuations or here-docs — adversarial users can `RUN
// echo VOLUME > /tmp/x` to bypass. The deny-list at the base-image
// level (pkg/imaged/base.go) catches the postgres/redis/mysql case
// which is the actually common one.
func scanDockerfileForStatefulShape(b []byte) string {
	scanner := bufio.NewScanner(bytes.NewReader(b))
	// Allow long lines (some RUN directives run thousands of chars).
	scanner.Buffer(make([]byte, 0, 64*1024), dockerfileMaxBytes)
	inRun := false
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		// Line continuations: a RUN that ends with `\` keeps reading.
		if inRun {
			if strings.Contains(line, "mkfs.ext4") || strings.Contains(line, "mkfs.xfs") ||
				strings.Contains(line, "mount -t ext4") || strings.Contains(line, "mount -t xfs") {
				return "mkfs/mount of a block device inside RUN"
			}
			if strings.HasSuffix(scanner.Text(), `\`) {
				continue
			}
			inRun = false
			continue
		}
		upper := strings.ToUpper(line)
		if strings.HasPrefix(upper, "VOLUME") {
			// VOLUME ["/data"] or VOLUME /data /var — extract the args.
			args := strings.Fields(line)
			if len(args) > 1 {
				return "VOLUME " + strings.Join(args[1:], " ")
			}
			return "VOLUME"
		}
		if strings.HasPrefix(upper, "RUN") {
			inRun = strings.HasSuffix(scanner.Text(), `\`)
			if strings.Contains(line, "mkfs.ext4") || strings.Contains(line, "mkfs.xfs") ||
				strings.Contains(line, "mount -t ext4") || strings.Contains(line, "mount -t xfs") {
				return "mkfs/mount of a block device inside RUN"
			}
		}
	}
	return ""
}

// truthyFlagLiterals are the string values the multipart dockerfile
// checkbox accepts as "yes". Centralised so goconst doesn't flag
// repeated literals across the file (and so a future "off"/"no"
// value addition is one line, not four).
var truthyFlagLiterals = []string{"1", "true", "on", "yes"}

// isFlagSet reads a small multipart field and reports whether it carries a
// truthy value (used by the dockerfile checkbox).
func isFlagSet(part *multipart.Part) bool {
	b, _ := io.ReadAll(io.LimitReader(part, 16))
	s := strings.ToLower(strings.TrimSpace(string(b)))
	for _, lit := range truthyFlagLiterals {
		if s == lit {
			return true
		}
	}
	return false
}
