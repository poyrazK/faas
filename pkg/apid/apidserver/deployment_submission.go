package apidserver

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/deploystaging"
	"github.com/onebox-faas/faas/pkg/apid/httpkit"
	"github.com/onebox-faas/faas/pkg/state"
)

type deploymentSubmissionRequestKey struct{}

func (s *server) getDeploymentSubmission(w http.ResponseWriter, r *http.Request, acct state.Account) {
	app, ok := s.loadApp(w, r, acct, r.PathValue("slug"))
	if !ok {
		return
	}
	id := r.PathValue("attempt_id")
	if !api.ValidDeploymentAttemptID(id) {
		api.WriteProblem(w, api.ErrValidation("attempt_id must be 32 lowercase hexadecimal characters"))
		return
	}
	store, ok := s.submissionStore(w)
	if !ok {
		return
	}
	receipt, err := store.DeploymentSubmission(r.Context(), app.ID, id)
	if errors.Is(err, state.ErrNotFound) {
		s.notFound(w, "no retained receipt for this deployment attempt; acceptance remains uncertain")
	} else if err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not read deployment submission receipt"))
	} else {
		writeJSON(w, http.StatusOK, receipt)
	}
}

func (s *server) submissionStore(w http.ResponseWriter) (state.DeploymentSubmissionStore, bool) {
	store, ok := s.store.(state.DeploymentSubmissionStore)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("deployment submission receipts are unavailable"))
	}
	return store, ok
}

// Receipts take precedence over stale HTTP cache reclamation. Only these
// app-scoped entry points can carry the creation context into the store.
func (s *server) withDeploymentSubmission(next accountHandler) accountHandler {
	return func(w http.ResponseWriter, r *http.Request, acct state.Account) {
		values := r.Header.Values(api.DeploymentAttemptHeader)
		if len(values) == 0 {
			next(w, r, acct)
			return
		}
		if len(values) != 1 || !api.ValidDeploymentAttemptID(values[0]) {
			api.WriteProblem(w, api.ErrValidation("Deployment-Attempt-ID must be 32 lowercase hexadecimal characters"))
			return
		}
		app, ok := s.loadApp(w, r, acct, r.PathValue("slug"))
		if !ok {
			return
		}
		store, ok := s.submissionStore(w)
		if !ok {
			return
		}
		receipt, err := store.DeploymentSubmission(r.Context(), app.ID, values[0])
		if err != nil && !errors.Is(err, state.ErrNotFound) {
			api.WriteProblem(w, api.ErrCapacity("could not verify deployment submission receipt"))
			return
		}
		ctx := context.WithValue(r.Context(), deploymentSubmissionRequestKey{}, true)
		ctx = state.ContextWithDeploymentSubmission(ctx, values[0])
		if strings.HasPrefix(r.Header.Get("Content-Type"), "multipart/form-data") {
			// Multipart handlers fingerprint the parsed fields and source,
			// then replay or refuse an accepted attempt themselves.
			if err == nil {
				ctx = context.WithValue(ctx, acceptedSubmissionKey{}, receipt)
			}
			next(w, r.WithContext(ctx), acct)
			return
		}
		fingerprint, ok := s.jsonSubmissionFingerprint(w, r)
		if !ok {
			return
		}
		if err == nil {
			s.replyAcceptedSubmission(w, r, app, receipt, fingerprint)
			return
		}
		if fingerprint != "" {
			ctx = state.ContextWithDeploymentSubmissionFingerprint(ctx, fingerprint)
		}
		next(w, r.WithContext(ctx), acct)
	}
}

type acceptedSubmissionKey struct{}

// jsonSubmissionFingerprint fingerprints a bounded JSON body and restores it for
// the handler. A body that is not one JSON value gets no fingerprint; the
// handler rejects it before any deployment exists.
func (s *server) jsonSubmissionFingerprint(w http.ResponseWriter, r *http.Request) (string, bool) {
	body, err := io.ReadAll(io.LimitReader(r.Body, httpkit.DefaultJSONBodyMaxBytes+1))
	if err != nil {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation, "Bad request", "could not read the request body"))
		return "", false
	}
	r.Body = io.NopCloser(io.MultiReader(bytes.NewReader(body), r.Body))
	if int64(len(body)) > httpkit.DefaultJSONBodyMaxBytes {
		return "", true
	}
	fingerprint, _ := deploystaging.JSONRequestFingerprint(r.Pattern, r.URL.Query(), body)
	return fingerprint, true
}

// replyMultipartSubmission replays or refuses an accepted attempt once a
// multipart request's fields and source are fingerprinted. Otherwise the
// caller carries the fingerprint to the receipt written with the deployment.
func (s *server) replyMultipartSubmission(w http.ResponseWriter, r *http.Request, app state.App, fingerprint string) bool {
	receipt, accepted := r.Context().Value(acceptedSubmissionKey{}).(api.DeploymentSubmissionReceipt)
	if accepted {
		s.replyAcceptedSubmission(w, r, app, receipt, fingerprint)
	}
	return accepted
}

func (s *server) replyAcceptedSubmission(w http.ResponseWriter, r *http.Request, app state.App, receipt api.DeploymentSubmissionReceipt, fingerprint string) {
	// Receipts accepted before fingerprints existed keep replaying.
	if receipt.RequestFingerprint != "" && receipt.RequestFingerprint != fingerprint {
		api.WriteProblem(w, api.NewProblem(http.StatusConflict, api.CodeDeploymentAttemptMismatch,
			"Deployment attempt reused", "this attempt was accepted for a different request; use a new Deployment-Attempt-ID for a new deployment"))
		return
	}
	d, err := s.store.DeploymentByID(r.Context(), receipt.DeploymentID)
	if err != nil || d.AppID != app.ID {
		api.WriteProblem(w, api.ErrCapacity("could not read accepted deployment"))
		return
	}
	w.Header().Set("Idempotent-Replayed", "true")
	writeJSON(w, http.StatusAccepted, s.deploymentResponse(r.Context(), d, app))
}

// A fresh saved attempt may reuse an older retry key. Link it durably to the
// cached deployment before returning the cached response; never replace a
// receipt that already names another deployment.
func (s *server) replayDeploymentSubmission(w http.ResponseWriter, r *http.Request, acct state.Account, status int, body []byte) {
	if attempted, _ := r.Context().Value(deploymentSubmissionRequestKey{}).(bool); attempted && status >= 200 && status < 300 {
		if !s.attachReplayedSubmission(w, r, acct, body) {
			return
		}
	}
	replayIdempotent(w, status, body)
}

func (s *server) attachReplayedSubmission(w http.ResponseWriter, r *http.Request, acct state.Account, body []byte) bool {
	var dep api.DeploymentResponse
	if json.Unmarshal(body, &dep) != nil || dep.ID == "" {
		api.WriteProblem(w, api.ErrCapacity("cached deployment has no acceptance identity"))
		return false
	}
	app, ok := s.loadApp(w, r, acct, r.PathValue("slug"))
	if !ok {
		return false
	}
	store, ok := s.submissionStore(w)
	if !ok {
		return false
	}
	_, err := store.AttachDeploymentSubmission(r.Context(), app.ID, r.Header.Get(api.DeploymentAttemptHeader), dep.ID)
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not save replayed deployment submission receipt; acceptance remains uncertain"))
		return false
	}
	return true
}
