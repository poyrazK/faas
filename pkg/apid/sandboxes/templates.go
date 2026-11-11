package sandboxes

// Sandbox templates (ADR-20261027090013 step 10, part three): a named image
// built from a Dockerfile in a builder microVM. Each template owns a
// platform-managed app that only builds; the CLI deploys the Dockerfile
// directory to that app, and sessions boot its newest finished build.

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/httpkit"
	"github.com/onebox-faas/faas/pkg/state"
	agentstore "github.com/onebox-faas/faas/pkg/state/agents"
)

// TemplateStore is the template state the handler needs.
type TemplateStore interface {
	CreateSandboxTemplate(ctx context.Context, accountID, name, appID string, spec agentstore.SandboxTemplateSpec) (string, error)
	UpdateSandboxTemplateSpec(ctx context.Context, accountID, name string, spec agentstore.SandboxTemplateSpec) error
	DeleteSandboxTemplate(ctx context.Context, accountID, name string) (string, error)
	SandboxTemplates(ctx context.Context, accountID string) ([]agentstore.SandboxTemplate, error)
	SandboxTemplate(ctx context.Context, accountID, name string) (agentstore.SandboxTemplate, error)
}

// TemplateApps creates and deletes a template's build app. apid owns apps,
// so the server supplies these; nil leaves templates unavailable.
type TemplateApps struct {
	Create func(ctx context.Context, r *http.Request, acct state.Account) (appID string, prob *api.Problem)
	Delete func(ctx context.Context, r *http.Request, acct state.Account, appID string) *api.Problem
}

const (
	templateCommandMaxBytes      = 4096
	templateReadyTimeoutDefaultS = 60
)

// PutTemplate handles PUT /v1/sandbox-templates/{name}: it creates the
// template and its build app, or replaces an existing template's commands.
func (h *Handler) PutTemplate(w http.ResponseWriter, r *http.Request, acct state.Account) {
	store, ok := h.templates(w)
	if !ok {
		return
	}
	name := r.PathValue("name")
	if !api.ValidSandboxTemplateName(name) {
		api.WriteProblem(w, api.ErrValidation("name must be 1-63 lowercase letters, digits and inner hyphens"))
		return
	}
	var body api.SandboxTemplateRequest
	if !httpkit.DecodeJSONLimit(w, r, &body, requestMaxBytes) {
		return
	}
	spec, prob := templateSpec(body)
	if prob != nil {
		api.WriteProblem(w, prob)
		return
	}
	created, prob := h.putTemplate(r, acct, store, name, spec)
	if prob != nil {
		api.WriteProblem(w, prob)
		return
	}
	t, err := store.SandboxTemplate(r.Context(), acct.ID, name)
	if err != nil {
		api.WriteProblem(w, h.problem(acct, "sandbox.template.read", err))
		return
	}
	status, action := http.StatusOK, "sandbox.template.updated"
	if created {
		status, action = http.StatusCreated, "sandbox.template.created"
	}
	h.auditTemplate(r.Context(), action, t)
	httpkit.WriteJSON(w, status, templateView(t))
}

// putTemplate updates an existing template, or creates its build app and
// then the template; a create that loses a race to another updates instead.
func (h *Handler) putTemplate(r *http.Request, acct state.Account, store TemplateStore, name string, spec agentstore.SandboxTemplateSpec) (bool, *api.Problem) {
	ctx := r.Context()
	if err := store.UpdateSandboxTemplateSpec(ctx, acct.ID, name, spec); !errors.Is(err, agentstore.ErrNotFound) {
		if err != nil {
			return false, h.problem(acct, "sandbox.template.update", err)
		}
		return false, nil
	}
	appID, prob := h.TemplateApps.Create(ctx, r, acct)
	if prob != nil {
		return false, prob
	}
	_, err := store.CreateSandboxTemplate(ctx, acct.ID, name, appID, spec)
	if err == nil {
		return true, nil
	}
	if prob := h.TemplateApps.Delete(ctx, r, acct, appID); prob != nil && h.Log != nil {
		h.Log.Warn("sandbox template: delete the unused build app", "app_id", appID, "detail", prob.Detail)
	}
	if errors.Is(err, agentstore.ErrTemplateExists) {
		if err := store.UpdateSandboxTemplateSpec(ctx, acct.ID, name, spec); err != nil {
			return false, h.problem(acct, "sandbox.template.update", err)
		}
		return false, nil
	}
	return false, h.problem(acct, "sandbox.template.create", err)
}

// ListTemplates handles GET /v1/sandbox-templates.
func (h *Handler) ListTemplates(w http.ResponseWriter, r *http.Request, acct state.Account) {
	store, ok := h.templates(w)
	if !ok {
		return
	}
	rows, err := store.SandboxTemplates(r.Context(), acct.ID)
	if err != nil {
		api.WriteProblem(w, h.problem(acct, "sandbox.template.list", err))
		return
	}
	out := api.SandboxTemplateList{Items: make([]api.SandboxTemplate, 0, len(rows))}
	for _, t := range rows {
		out.Items = append(out.Items, templateView(t))
	}
	httpkit.WriteJSON(w, http.StatusOK, out)
}

// GetTemplate handles GET /v1/sandbox-templates/{name}.
func (h *Handler) GetTemplate(w http.ResponseWriter, r *http.Request, acct state.Account) {
	store, ok := h.templates(w)
	if !ok {
		return
	}
	name := r.PathValue("name")
	t, err := store.SandboxTemplate(r.Context(), acct.ID, name)
	if errors.Is(err, agentstore.ErrNotFound) {
		api.WriteProblem(w, api.ErrSandboxTemplateNotFound(name))
		return
	}
	if err != nil {
		api.WriteProblem(w, h.problem(acct, "sandbox.template.read", err))
		return
	}
	httpkit.WriteJSON(w, http.StatusOK, templateView(t))
}

// DeleteTemplate handles DELETE /v1/sandbox-templates/{name}: the build app
// is deleted first, so a failure leaves the template whole to retry. Live
// sessions keep their private copy of the image.
func (h *Handler) DeleteTemplate(w http.ResponseWriter, r *http.Request, acct state.Account) {
	store, ok := h.templates(w)
	if !ok {
		return
	}
	name := r.PathValue("name")
	t, err := store.SandboxTemplate(r.Context(), acct.ID, name)
	if errors.Is(err, agentstore.ErrNotFound) {
		api.WriteProblem(w, api.ErrSandboxTemplateNotFound(name))
		return
	}
	if err != nil {
		api.WriteProblem(w, h.problem(acct, "sandbox.template.read", err))
		return
	}
	if prob := h.TemplateApps.Delete(r.Context(), r, acct, t.AppID); prob != nil {
		api.WriteProblem(w, prob)
		return
	}
	if _, err := store.DeleteSandboxTemplate(r.Context(), acct.ID, name); err != nil && !errors.Is(err, agentstore.ErrNotFound) {
		api.WriteProblem(w, h.problem(acct, "sandbox.template.delete", err))
		return
	}
	h.auditTemplate(r.Context(), "sandbox.template.deleted", t)
	w.WriteHeader(http.StatusNoContent)
}

// templates answers capacity_unavailable on an apid without template state.
func (h *Handler) templates(w http.ResponseWriter) (TemplateStore, bool) {
	store, ok := h.Store.(TemplateStore)
	if !ok || h.TemplateApps.Create == nil || h.TemplateApps.Delete == nil {
		api.WriteProblem(w, api.ErrCapacity("sandbox templates are unavailable on this control-plane host"))
		return nil, false
	}
	return store, true
}

func templateSpec(body api.SandboxTemplateRequest) (agentstore.SandboxTemplateSpec, *api.Problem) {
	spec := agentstore.SandboxTemplateSpec{StartCmd: body.StartCmd, ReadyCmd: body.ReadyCmd, ReadyTimeoutSec: body.ReadyTimeoutS}
	if spec.ReadyTimeoutSec == 0 {
		spec.ReadyTimeoutSec = templateReadyTimeoutDefaultS
	}
	switch {
	case len(spec.StartCmd) > templateCommandMaxBytes || len(spec.ReadyCmd) > templateCommandMaxBytes:
		return spec, api.ErrValidation(fmt.Sprintf("start_cmd and ready_cmd must be at most %d bytes", templateCommandMaxBytes))
	case spec.ReadyTimeoutSec < 1 || spec.ReadyTimeoutSec > api.SandboxTemplateReadyTimeoutSecMax:
		return spec, api.ErrValidation(fmt.Sprintf("ready_timeout_s must be 1-%d (SandboxTemplateReadyTimeoutSecMax)", api.SandboxTemplateReadyTimeoutSecMax))
	}
	return spec, nil
}

func templateView(t agentstore.SandboxTemplate) api.SandboxTemplate {
	out := api.SandboxTemplate{Name: t.Name, App: t.AppSlug, StartCmd: t.StartCmd, ReadyCmd: t.ReadyCmd,
		ReadyTimeoutS: t.ReadyTimeoutSec, CreatedAt: t.CreatedAt, UpdatedAt: t.UpdatedAt, Ready: t.Ready(), BuiltAt: t.LiveBuiltAt}
	if t.BuildID != "" {
		out.Build = &api.SandboxTemplateBuild{ID: t.BuildID, Status: t.BuildStatus, Error: t.BuildError}
	}
	return out
}

func (h *Handler) auditTemplate(ctx context.Context, action string, t agentstore.SandboxTemplate) {
	if h.Audit != nil {
		h.Audit(ctx, action, t.AccountID, map[string]any{"template": t.Name, "app_id": t.AppID})
	}
}
