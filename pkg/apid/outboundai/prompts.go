package outboundai

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/httpkit"
	"github.com/onebox-faas/faas/pkg/state"
)

// Managed prompts (ADR 20261114180740).

const promptTargetDetail = "the target must be one of this account's openai-format routes, or one of its enabled managed AI " +
	"integrations an openai-format route accepts (openai, azure_openai, anthropic, gemini, bedrock or a located vertex), " +
	"with a model for every provider but openai"

func (s *Service) promptStore(w http.ResponseWriter) (state.OutboundAIPromptStore, bool) {
	store, ok := s.Host.Store().(state.OutboundAIPromptStore)
	if !ok {
		problem(w, http.StatusServiceUnavailable, "outbound_ai_prompts_unavailable", "Managed AI prompts are unavailable")
	}
	return store, ok
}

func promptPathName(w http.ResponseWriter, r *http.Request) (string, bool) {
	name := r.PathValue("prompt")
	if !api.ValidOutboundAIPromptName(name) {
		problem(w, http.StatusBadRequest, api.CodeValidation, "Prompt name must be a DNS label")
		return "", false
	}
	return name, true
}

func promptPathVersion(w http.ResponseWriter, r *http.Request) (int, bool) {
	version, err := strconv.Atoi(r.PathValue("version"))
	if err != nil || version < 1 {
		problem(w, http.StatusBadRequest, api.CodeValidation, "Version must be a positive integer")
		return 0, false
	}
	return version, true
}

func promptResponse(prompt state.OutboundAIPrompt) api.OutboundAIPrompt {
	out := api.OutboundAIPrompt{ID: prompt.ID, Name: prompt.Name, Description: prompt.Description,
		LiveVersion: copyInt(prompt.LiveVersion), PreviousLiveVersion: copyInt(prompt.PreviousLiveVersion),
		LatestVersion: prompt.LatestVersion, CreatedAt: prompt.CreatedAt, UpdatedAt: prompt.UpdatedAt}
	if prompt.Canary != nil {
		canary := promptCanaryResponse(*prompt.Canary)
		out.Canary = &canary
	}
	return out
}

func promptVersionResponse(version state.OutboundAIPromptVersion) api.OutboundAIPromptVersion {
	defaults := version.Defaults
	if defaults == nil {
		defaults = map[string]string{}
	}
	return api.OutboundAIPromptVersion{Version: version.Version, Messages: version.Messages,
		Variables: api.OutboundAIPromptVariables(version.Messages), Defaults: defaults, Target: version.Target,
		Params: version.Params, Note: version.Note, CreatedAt: version.CreatedAt}
}

// writePromptError maps a prompt store error; it reports whether it wrote.
func writePromptError(w http.ResponseWriter, err error, notFound, invalid string) bool {
	switch {
	case err == nil:
		return false
	case errors.Is(err, state.ErrNotFound):
		httpkit.NotFound(w, notFound)
	case errors.Is(err, state.ErrInvalidArgument):
		problem(w, http.StatusBadRequest, api.CodeValidation, invalid)
	case errors.Is(err, state.ErrOutboundAIPromptLimit):
		problem(w, http.StatusTooManyRequests, "outbound_ai_prompt_limit", "The account has reached its plan's managed prompt limit")
	case errors.Is(err, state.ErrConflict):
		problem(w, http.StatusConflict, api.CodeConflict, "The prompt changed meanwhile; retry")
	default:
		problem(w, http.StatusServiceUnavailable, "outbound_ai_prompts_unavailable", "Managed AI prompts are unavailable")
	}
	return true
}

func writePromptJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Cache-Control", "no-store")
	httpkit.WriteJSON(w, status, body)
}

func (s *Service) ListPrompts(w http.ResponseWriter, r *http.Request, acct state.Account) {
	store, ok := s.promptStore(w)
	if !ok {
		return
	}
	prompts, err := store.ListOutboundAIPrompts(r.Context(), acct.ID)
	if writePromptError(w, err, "", "") {
		return
	}
	out := api.OutboundAIPromptList{Items: make([]api.OutboundAIPrompt, 0, len(prompts))}
	for _, prompt := range prompts {
		out.Items = append(out.Items, promptResponse(prompt))
	}
	writePromptJSON(w, http.StatusOK, out)
}

func (s *Service) GetPrompt(w http.ResponseWriter, r *http.Request, acct state.Account) {
	name, ok := promptPathName(w, r)
	if !ok {
		return
	}
	store, ok := s.promptStore(w)
	if !ok {
		return
	}
	prompt, err := store.GetOutboundAIPrompt(r.Context(), acct.ID, name)
	if writePromptError(w, err, "AI prompt not found", "") {
		return
	}
	writePromptJSON(w, http.StatusOK, promptResponse(prompt))
}

// PushPromptVersion stores a version, creating the prompt on its first one.
// A push identical to the latest version answers 200 with created false.
func (s *Service) PushPromptVersion(w http.ResponseWriter, r *http.Request, acct state.Account) {
	name, ok := promptPathName(w, r)
	if !ok {
		return
	}
	var req api.PushOutboundAIPromptVersionRequest
	if err := httpkit.DecodeJSONSized(r, &req, api.OutboundAIPromptPushBodyMaxBytes); err != nil {
		problem(w, http.StatusBadRequest, api.CodeValidation, "The body must be a prompt version of at most 128 KiB")
		return
	}
	if detail := req.Problem(); detail != "" {
		problem(w, http.StatusBadRequest, api.CodeValidation, detail)
		return
	}
	store, ok := s.promptStore(w)
	if !ok {
		return
	}
	limits, _ := api.LimitsFor(acct.Plan)
	prompt, version, created, err := store.PushOutboundAIPromptVersion(r.Context(), state.OutboundAIPromptPush{
		ID: uuid.NewString(), AccountID: acct.ID, Name: name, Description: req.Description, Live: req.Live,
		MaxPrompts: limits.OutboundAIPromptsMax,
		Version: state.OutboundAIPromptVersion{Messages: req.Messages, Defaults: req.Defaults, Params: req.Params,
			Target: req.Target, Note: req.Note},
	})
	if writePromptError(w, err, "AI prompt not found", promptTargetDetail) {
		return
	}
	status := http.StatusOK
	if created {
		status = http.StatusCreated
	}
	writePromptJSON(w, status, api.PushOutboundAIPromptVersionResponse{Prompt: promptResponse(prompt),
		Version: promptVersionResponse(version), Created: created})
}

func (s *Service) ListPromptVersions(w http.ResponseWriter, r *http.Request, acct state.Account) {
	name, ok := promptPathName(w, r)
	if !ok {
		return
	}
	store, ok := s.promptStore(w)
	if !ok {
		return
	}
	versions, err := store.ListOutboundAIPromptVersions(r.Context(), acct.ID, name)
	if writePromptError(w, err, "AI prompt not found", "") {
		return
	}
	out := api.OutboundAIPromptVersionList{Items: make([]api.OutboundAIPromptVersionSummary, 0, len(versions))}
	for _, version := range versions {
		out.Items = append(out.Items, api.OutboundAIPromptVersionSummary{Version: version.Version, Target: version.Target,
			Note: version.Note, CreatedAt: version.CreatedAt})
	}
	writePromptJSON(w, http.StatusOK, out)
}

func (s *Service) GetPromptVersion(w http.ResponseWriter, r *http.Request, acct state.Account) {
	name, ok := promptPathName(w, r)
	if !ok {
		return
	}
	number, ok := promptPathVersion(w, r)
	if !ok {
		return
	}
	store, ok := s.promptStore(w)
	if !ok {
		return
	}
	version, err := store.GetOutboundAIPromptVersion(r.Context(), acct.ID, name, number)
	if writePromptError(w, err, "AI prompt version not found", "") {
		return
	}
	writePromptJSON(w, http.StatusOK, promptVersionResponse(version))
}

// PutPromptLive makes a version live; the old live version becomes the
// previous one and any canary ends.
func (s *Service) PutPromptLive(w http.ResponseWriter, r *http.Request, acct state.Account) {
	name, ok := promptPathName(w, r)
	if !ok {
		return
	}
	var req api.PutOutboundAIPromptLiveRequest
	if err := httpkit.DecodeJSONSized(r, &req, 1<<10); err != nil || req.Version < 1 {
		problem(w, http.StatusBadRequest, api.CodeValidation, "The body must name a version")
		return
	}
	store, ok := s.promptStore(w)
	if !ok {
		return
	}
	prompt, err := store.SetOutboundAIPromptLive(r.Context(), acct.ID, name, req.Version)
	if writePromptError(w, err, "AI prompt not found", "The prompt has no such version") {
		return
	}
	writePromptJSON(w, http.StatusOK, promptResponse(prompt))
}

// RollBackPrompt puts the previous live version back.
func (s *Service) RollBackPrompt(w http.ResponseWriter, r *http.Request, acct state.Account) {
	name, ok := promptPathName(w, r)
	if !ok {
		return
	}
	store, ok := s.promptStore(w)
	if !ok {
		return
	}
	prompt, err := store.RollBackOutboundAIPromptLive(r.Context(), acct.ID, name)
	if writePromptError(w, err, "AI prompt not found", "The prompt has no previous live version to roll back to") {
		return
	}
	writePromptJSON(w, http.StatusOK, promptResponse(prompt))
}

func (s *Service) DeletePrompt(w http.ResponseWriter, r *http.Request, acct state.Account) {
	name, ok := promptPathName(w, r)
	if !ok {
		return
	}
	store, ok := s.promptStore(w)
	if !ok {
		return
	}
	err := store.DeleteOutboundAIPrompt(r.Context(), acct.ID, name)
	if writePromptError(w, err, "AI prompt not found", "") {
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusNoContent)
}
