package main

import (
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// scanProjectSourceRef is the connected-repository counterpart to scanProject.
// githubd owns the short-lived installation credential and streams the archive
// into a disk-backed multipart request so the normal scanner remains the single
// source of plan semantics, limits, and plan-token generation.
func (s *server) scanProjectSourceRef(w http.ResponseWriter, r *http.Request, acct state.Account) {
	req, problem := decodeProjectSourceRefScanRequest(r)
	if problem != nil {
		api.WriteProblem(w, problem)
		return
	}
	installID, problem := s.projectScanInstallation(r, acct, req)
	if problem != nil {
		api.WriteProblem(w, problem)
		return
	}
	scanRequest, cleanup, problem := s.projectSourceRefMultipart(r, acct, req, installID)
	if problem != nil {
		api.WriteProblem(w, problem)
		return
	}
	defer cleanup()
	resp, _, _, _, _, _, problem := s.scanService(scanRequest, acct, "", false)
	if problem != nil {
		api.WriteProblem(w, problem)
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

func decodeProjectSourceRefScanRequest(r *http.Request) (api.ProjectSourceRefScanRequest, *api.Problem) {
	var req api.ProjectSourceRefScanRequest
	decoder := json.NewDecoder(io.LimitReader(r.Body, 1<<20))
	if err := decoder.Decode(&req); err != nil {
		return req, api.NewProblem(http.StatusBadRequest, api.CodeValidation, "Bad request", err.Error())
	}
	req.Repo = strings.TrimSpace(req.Repo)
	req.Ref = strings.TrimSpace(req.Ref)
	req.ProjectSlug = strings.TrimSpace(req.ProjectSlug)
	req.RepoFullName = strings.TrimSpace(req.RepoFullName)
	req.ProductionBranch = strings.TrimSpace(req.ProductionBranch)
	if req.RepoFullName == "" {
		req.RepoFullName = req.Repo
	}
	if req.ProductionBranch == "" {
		req.ProductionBranch = "main"
	}
	if problem := validateProjectSourceRefScanRequest(req); problem != nil {
		return req, problem
	}
	return req, nil
}

func validateProjectSourceRefScanRequest(req api.ProjectSourceRefScanRequest) *api.Problem {
	invalid := func(detail string) *api.Problem {
		return api.NewProblem(http.StatusBadRequest, api.CodeValidation, "Validation failed", detail)
	}
	if !validProjectRepoFullName(req.Repo) || !validProjectRepoFullName(req.RepoFullName) {
		return invalid("repo and repo_full_name must be GitHub owner/name slugs")
	}
	if !isValidRef(req.Ref) || !isValidRef(req.ProductionBranch) {
		return invalid("ref and production_branch must be valid Git ref names")
	}
	if !api.ValidProjectSlug(req.ProjectSlug) {
		return invalid("project_slug must contain 1-63 lowercase letters, digits, or internal hyphens")
	}
	if req.InstallID < 0 {
		return invalid("install_id must be zero or a positive integer")
	}
	return nil
}

func (s *server) projectScanInstallation(r *http.Request, acct state.Account, req api.ProjectSourceRefScanRequest) (int64, *api.Problem) {
	if req.InstallID > 0 {
		return s.requireOwnedGitHubInstallation(r.Context(), acct.ID, req.InstallID)
	}
	return s.resolveRepositoryInstallation(r.Context(), acct.ID, req.Repo)
}

func (s *server) projectSourceRefMultipart(r *http.Request, acct state.Account, req api.ProjectSourceRefScanRequest, installID int64) (*http.Request, func(), *api.Problem) {
	limits := api.MustLimitsFor(acct.Plan)
	maxBytes := int64(limits.SourceTarballMaxMB) * 1024 * 1024
	stream, problem := s.streamSourceTarball(r.Context(), acct, installID, req.Repo, req.Ref, maxBytes)
	if problem != nil {
		return nil, func() {}, problem
	}
	if err := os.MkdirAll(scanSpoolRoot(), 0o700); err != nil {
		_ = stream.Body.Close()
		return nil, func() {}, api.ErrCapacity("could not create project scan spool directory")
	}
	tmp, err := os.CreateTemp(scanSpoolRoot(), "source-ref-*.multipart")
	if err != nil {
		_ = stream.Body.Close()
		return nil, func() {}, api.ErrCapacity("could not create project scan spool")
	}
	cleanup := func() { _ = tmp.Close(); _ = os.Remove(tmp.Name()) }
	writer := multipart.NewWriter(tmp)
	if problem = copyProjectSourceRefArchive(writer, stream, req.Repo, maxBytes, limits); problem != nil {
		cleanup()
		return nil, func() {}, problem
	}
	if err = writeProjectSourceRefFields(writer, req, installID); err == nil {
		err = writer.Close()
	}
	if err == nil {
		_, err = tmp.Seek(0, io.SeekStart)
	}
	if err != nil {
		cleanup()
		return nil, func() {}, api.ErrCapacity("could not prepare project scan request")
	}
	synthetic := r.Clone(r.Context())
	synthetic.Body = tmp
	synthetic.Header.Set("Content-Type", writer.FormDataContentType())
	return synthetic, cleanup, nil
}

func copyProjectSourceRefArchive(writer *multipart.Writer, stream *StreamSourceRefResult, repo string, maxBytes int64, limits api.Limits) *api.Problem {
	fileName := filepath.Base(repo) + ".tar.gz"
	part, err := writer.CreateFormFile("source", fileName)
	if err != nil {
		_ = stream.Body.Close()
		return api.ErrCapacity("could not prepare project scan archive")
	}
	written, copyErr := io.Copy(part, io.LimitReader(stream.Body, maxBytes+1))
	closeErr := stream.Body.Close()
	if written > maxBytes || stream.Stats != nil && stream.Stats.Truncated {
		return api.ErrSourceTooLarge(limits, written)
	}
	if copyErr != nil || closeErr != nil || stream.Stats != nil && stream.Stats.Err != nil {
		return projectSourceRefStreamProblem(copyErr, closeErr, stream.Stats)
	}
	return nil
}

func projectSourceRefStreamProblem(copyErr, closeErr error, stats *StreamSourceRefStats) *api.Problem {
	for _, err := range []error{copyErr, closeErr} {
		if problem := api.AsProblem(err); problem != nil {
			return problem
		}
	}
	if stats != nil {
		if problem := api.AsProblem(stats.Err); problem != nil {
			return problem
		}
	}
	return api.ErrSourceRefUnavailable("source stream ended with an error")
}

func writeProjectSourceRefFields(writer *multipart.Writer, req api.ProjectSourceRefScanRequest, installID int64) error {
	fields := map[string]string{
		"project_slug":      req.ProjectSlug,
		"repo_full_name":    req.RepoFullName,
		"production_branch": req.ProductionBranch,
		"install_id":        strconv.FormatInt(installID, 10),
		"only":              strings.Join(req.Only, ","),
		"exclude":           strings.Join(req.Exclude, ","),
		"no_triggers":       strconv.FormatBool(req.NoTriggers),
	}
	for name, value := range fields {
		if err := writer.WriteField(name, value); err != nil {
			return fmt.Errorf("write %s: %w", name, err)
		}
	}
	return nil
}
