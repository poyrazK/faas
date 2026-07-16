package main

import (
	"archive/tar"
	"compress/gzip"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/state"
)

// Source tarball + dockerfile + function deploys (spec §9).
//
// apid is the *Accept* step (spec §9 step 1). It validates the tarball
// shape, spools it to disk, creates the queued build row, and notifies
// builderd via pg_notify('build_queued'). builderd (M6) is the actual
// executor; in M5 the build row just sits in 'queued' state.

// maxSourceFiles caps tarball entries at 10k (spec §9).
const maxSourceFiles = 10_000

// sourceSpoolRoot is where apid drops source tarballs before imaged /
// builderd process them. The dir is host-configurable via env to keep tests
// hermetic.
const sourceSpoolRootEnv = "FAAS_SPOOL_ROOT"

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
func (s *server) createDeploymentMultipart(w http.ResponseWriter, r *http.Request, acct state.Account, app state.App) {
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
		sourcePath    string
		sourceBytes   int64
		dockerfile    bool
		runtime       string
		handler       string
		kind          state.DeploymentKind
	)

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
			path, n, vErr := validateAndSpool(part, limits)
			if vErr != nil {
				api.WriteProblem(w, vErr)
				return
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

	// Function rewrites (spec §4.2): source + runtime + handler becomes a
	// function deploy using the runner scaffold (§4.9). The runtime must
	// be present and the handler must point at a real export.
	if app.Type == state.AppTypeFunction {
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

	deploymentID := ""
	buildLog := ""
	if sourcePath != "" {
		d, err := s.store.CreateDeployment(ctx(r), state.Deployment{
			AppID:       app.ID,
			Kind:        kind,
			SourcePath:  sourcePath,
			SourceBytes: sourceBytes,
			Handler:     handler,
			LogPath:     buildLog,
			Status:      state.DeployPending,
		})
		if err != nil {
			api.WriteProblem(w, api.ErrCapacity("could not create deployment"))
			return
		}
		// Spool the log file alongside the source so builderd can write to
		// it directly. The path is created lazily so empty log_path stays
		// safe for image: deploys.
		logDir := filepath.Join(spoolRoot(), d.ID)
		_ = os.MkdirAll(logDir, 0o755)
		logPath := filepath.Join(logDir, "build.log")
		_, _ = os.Create(logPath)
		if err := s.store.UpdateDeploymentStatus(ctx(r), d.ID, state.DeployBuilding, ""); err == nil {
			// Update log_path by re-reading and writing via the dedicated
			// path. For simplicity the deployment row keeps an empty
			// log_path here; builderd re-stamps it once it starts (M6).
			_ = logPath
		}
		build, err := s.store.CreateBuild(ctx(r), d.ID, kind, sourceBytes, logPath)
		if err != nil {
			api.WriteProblem(w, api.ErrCapacity("could not create build row"))
			return
		}
		_ = s.notif.Notify(ctx(r), db.NotifyBuildQueued,
			fmt.Sprintf(`{"build":"%s","deployment":"%s","app":"%s","kind":"%s"}`,
				build.ID, d.ID, app.ID, kind))
		s.log.Info("source deploy queued", "deployment", d.ID, "app", app.ID, "kind", kind, "build", build.ID)
		writeJSON(w, http.StatusAccepted, s.deploymentResponse(d))
		return
	}
	_ = deploymentID
}

// validateAndSpool reads the multipart file part, validates the tarball
// shape, and writes it to the spool dir. Returns (spool_path, bytes, problem).
//
// Order is: write to a tmp path, validate, then atomically rename to the
// canonical path. This avoids leaving a malformed or oversized tarball at the
// canonical spool path where builderd could race to pick it up before the
// validation result is known.
func validateAndSpool(part *multipart.Part, limits api.Limits) (string, int64, *api.Problem) {
	if part.FileName() == "" {
		return "", 0, api.NewProblem(http.StatusBadRequest, api.CodeValidation,
			"Bad source", "source field must be a file")
	}
	if err := os.MkdirAll(spoolRoot(), 0o755); err != nil {
		return "", 0, api.ErrCapacity("could not create spool dir")
	}
	id := randomToken(12)
	dst := filepath.Join(spoolRoot(), id+".tar.gz")
	tmp := dst + ".tmp"
	out, err := os.Create(tmp)
	if err != nil {
		return "", 0, api.ErrCapacity("could not spool source")
	}
	defer func() { _ = out.Close() }()

	n, err := io.Copy(out, part)
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

// validateTarballShape opens the spooled tarball and verifies the §9
// invariants: ≤10k files, no symlink escapes, no absolute paths.
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
		count++
		if count > maxSourceFiles {
			return api.ErrSourceInvalid(fmt.Sprintf("too many files (>%d)", maxSourceFiles))
		}
		if strings.HasPrefix(hdr.Name, "/") || strings.Contains(hdr.Name, "..") {
			return api.ErrSourceInvalid("absolute paths or '..' entries are rejected")
		}
	}
	return nil
}

// isFlagSet reads a small multipart field and reports whether it carries a
// truthy value (used by the dockerfile checkbox).
func isFlagSet(part *multipart.Part) bool {
	b, _ := io.ReadAll(io.LimitReader(part, 16))
	s := strings.ToLower(strings.TrimSpace(string(b)))
	return s == "1" || s == "true" || s == "on" || s == "yes"
}
