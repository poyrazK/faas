// Package projectsecrets is apid's surface for secrets shared by every
// service of a project environment (ADR-1139): list, set, rotate and delete.
// Values are write-only; responses carry metadata only.
//
// adr: 1139
package projectsecrets

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/httpkit"
	"github.com/onebox-faas/faas/pkg/state"
)

// Sealed is one value sealed for the fleet: a single-key age envelope, the
// recipient that sealed it and the value's HMAC fingerprint.
type Sealed struct {
	Ciphertext     []byte
	Kid, ValueHash string
}

// Host is what the project secrets surface needs from apid.
type Host interface {
	Store() state.Store
	EmitAudit(ctx context.Context, kind string, accountID *string, data map[string]any)
	// Seal seals key's value, or returns the problem to answer with (sealing
	// unavailable, value over maxBytes).
	Seal(accountID, key, value string, maxBytes int) (Sealed, *api.Problem)
}

// Service is the project secrets surface.
type Service struct{ Host Host }

// maxBodyBytes bounds a write body: the largest plan value plus JSON framing.
const maxBodyBytes = 64 << 10

func (s Service) List(w http.ResponseWriter, r *http.Request, acct state.Account) {
	secrets, project, environment, ok := s.load(w, r, acct)
	if !ok {
		return
	}
	rows, err := secrets.ListProjectEnvironmentSecrets(r.Context(), acct.ID, project.ID, environment.Slug)
	if err != nil {
		writeStoreProblem(w, err, project.Slug, environment.Slug)
		return
	}
	out := make([]api.ProjectEnvironmentSharedSecret, 0, len(rows))
	for _, row := range rows {
		out = append(out, toAPI(row))
	}
	httpkit.WriteJSON(w, http.StatusOK, api.ProjectEnvironmentSharedSecretsResponse{
		ProjectSlug: project.Slug, Environment: environment.Slug, Secrets: out,
		Limit: api.MustLimitsFor(acct.Plan).ProjectSecretCountMax,
	})
}

// Set creates the key or replaces its value.
func (s Service) Set(w http.ResponseWriter, r *http.Request, acct state.Account) {
	s.write(w, r, acct, false)
}

// Rotate replaces an existing key's value; an unknown key is 404.
func (s Service) Rotate(w http.ResponseWriter, r *http.Request, acct state.Account) {
	s.write(w, r, acct, true)
}

func (s Service) write(w http.ResponseWriter, r *http.Request, acct state.Account, rotate bool) {
	key := r.PathValue("key")
	if problem := validKey(key); problem != nil {
		api.WriteProblem(w, problem)
		return
	}
	var req api.SetProjectEnvironmentSharedSecretRequest
	if !httpkit.DecodeJSONLimit(w, r, &req, maxBodyBytes) {
		return
	}
	if req.Value == "" {
		api.WriteProblem(w, api.ErrValidation("value is required"))
		return
	}
	secrets, project, environment, ok := s.load(w, r, acct)
	if !ok {
		return
	}
	if rotate && !s.exists(w, r, secrets, acct, project, environment, key) {
		return
	}
	limits := api.MustLimitsFor(acct.Plan)
	sealed, problem := s.Host.Seal(acct.ID, key, req.Value, limits.SecretValueMaxBytes)
	if problem != nil {
		api.WriteProblem(w, problem)
		return
	}
	stored, err := secrets.SetProjectEnvironmentSecret(r.Context(), state.ProjectEnvironmentSecret{
		AccountID: acct.ID, ProjectID: project.ID, EnvironmentSlug: environment.Slug, Key: key,
		Ciphertext: sealed.Ciphertext, Kid: sealed.Kid, ValueHash: sealed.ValueHash,
	}, limits.ProjectSecretCountMax)
	if errors.Is(err, state.ErrProjectEnvironmentSecretLimit) {
		api.WriteProblem(w, api.ErrPlanLimitProjectSecrets(limits, limits.ProjectSecretCountMax))
		return
	}
	if err != nil {
		writeStoreProblem(w, err, project.Slug, environment.Slug)
		return
	}
	kind := "project.secret.set"
	if rotate {
		kind = "project.secret.rotated"
	}
	s.audit(r.Context(), kind, acct, project, environment, map[string]any{"key": key, "version": stored.SecretVersion, "kid": stored.Kid})
	httpkit.WriteJSON(w, http.StatusOK, api.ProjectEnvironmentSharedSecretResponse{
		ProjectSlug: project.Slug, Environment: environment.Slug, Secret: toAPI(stored),
	})
}

func (s Service) Delete(w http.ResponseWriter, r *http.Request, acct state.Account) {
	key := r.PathValue("key")
	if problem := api.ValidateSecretKey(key); problem != nil {
		api.WriteProblem(w, problem)
		return
	}
	secrets, project, environment, ok := s.load(w, r, acct)
	if !ok {
		return
	}
	err := secrets.DeleteProjectEnvironmentSecret(r.Context(), acct.ID, project.ID, environment.Slug, key)
	if errors.Is(err, state.ErrNotFound) {
		api.WriteProblem(w, secretNotFound(project.Slug, environment.Slug, key))
		return
	}
	if err != nil {
		writeStoreProblem(w, err, project.Slug, environment.Slug)
		return
	}
	s.audit(r.Context(), "project.secret.deleted", acct, project, environment, map[string]any{"key": key})
	w.WriteHeader(http.StatusNoContent)
}

func (s Service) load(w http.ResponseWriter, r *http.Request, acct state.Account) (state.ProjectEnvironmentSecretStore, state.Project, state.ProjectEnvironment, bool) {
	store := s.Host.Store()
	secrets, ok := store.(state.ProjectEnvironmentSecretStore)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("project environment secrets are unavailable"))
		return nil, state.Project{}, state.ProjectEnvironment{}, false
	}
	projectSlug, environmentSlug := r.PathValue("slug"), r.PathValue("environment")
	project, err := store.ProjectBySlug(r.Context(), acct.ID, projectSlug)
	if errors.Is(err, state.ErrNotFound) {
		api.WriteProblem(w, api.NewProblem(http.StatusNotFound, api.CodeNotFound, "Project not found", "no project exists with slug "+projectSlug))
		return nil, state.Project{}, state.ProjectEnvironment{}, false
	}
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not load project"))
		return nil, state.Project{}, state.ProjectEnvironment{}, false
	}
	environment, err := store.ProjectEnvironmentBySlug(r.Context(), acct.ID, project.ID, environmentSlug)
	if errors.Is(err, state.ErrNotFound) {
		api.WriteProblem(w, environmentNotFound(project.Slug, environmentSlug))
		return nil, state.Project{}, state.ProjectEnvironment{}, false
	}
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not load project environment"))
		return nil, state.Project{}, state.ProjectEnvironment{}, false
	}
	return secrets, project, environment, true
}

func (s Service) exists(w http.ResponseWriter, r *http.Request, secrets state.ProjectEnvironmentSecretStore, acct state.Account, project state.Project, environment state.ProjectEnvironment, key string) bool {
	rows, err := secrets.ListProjectEnvironmentSecrets(r.Context(), acct.ID, project.ID, environment.Slug)
	if err != nil {
		writeStoreProblem(w, err, project.Slug, environment.Slug)
		return false
	}
	for _, row := range rows {
		if row.Key == key {
			return true
		}
	}
	api.WriteProblem(w, secretNotFound(project.Slug, environment.Slug, key))
	return false
}

func (s Service) audit(ctx context.Context, kind string, acct state.Account, project state.Project, environment state.ProjectEnvironment, data map[string]any) {
	data["project_id"], data["project_slug"] = project.ID, project.Slug
	data["environment_id"], data["environment_slug"] = environment.ID, environment.Slug
	s.Host.EmitAudit(ctx, kind, &acct.ID, data)
}

// validKey is the app secret key rule plus the platform identity names, which
// a shared value must never shadow.
func validKey(key string) *api.Problem {
	if problem := api.ValidateSecretKey(key); problem != nil {
		return problem
	}
	if api.IsPlatformIdentityEnvKey(key) {
		return api.ErrSecretInvalidKey(key + " is reserved for platform identity")
	}
	return nil
}

func toAPI(secret state.ProjectEnvironmentSecret) api.ProjectEnvironmentSharedSecret {
	return api.ProjectEnvironmentSharedSecret{
		Key: secret.Key, Version: secret.SecretVersion, Kid: secret.Kid, ValueHash: secret.ValueHash,
		CreatedAt: secret.CreatedAt.UTC().Format(time.RFC3339Nano), UpdatedAt: secret.UpdatedAt.UTC().Format(time.RFC3339Nano),
	}
}

func writeStoreProblem(w http.ResponseWriter, err error, projectSlug, environmentSlug string) {
	switch {
	case errors.Is(err, state.ErrNotFound):
		api.WriteProblem(w, environmentNotFound(projectSlug, environmentSlug))
	case errors.Is(err, state.ErrConflict):
		api.WriteProblem(w, api.NewProblem(http.StatusConflict, api.CodeConflict, "Secret could not be stored",
			"the sealing key is being retired; retry in a few seconds"))
	default:
		api.WriteProblem(w, api.ErrCapacity("could not store project environment secret"))
	}
}

func environmentNotFound(projectSlug, environmentSlug string) *api.Problem {
	return api.NewProblem(http.StatusNotFound, api.CodeNotFound,
		"Project environment not found", "no environment "+environmentSlug+" exists in project "+projectSlug)
}

func secretNotFound(projectSlug, environmentSlug, key string) *api.Problem {
	return api.NewProblem(http.StatusNotFound, api.CodeNotFound,
		"Project secret not found", "no secret "+key+" exists in environment "+environmentSlug+" of project "+projectSlug)
}
