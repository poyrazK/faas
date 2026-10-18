// Package responsecheck implements owner-authorized, deployment-specific GET
// checks. It delegates routing and wake to gatewayd, never directly to VMMD.
package responsecheck

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/httpkit"
	"github.com/onebox-faas/faas/pkg/deploymentcheck"
	"github.com/onebox-faas/faas/pkg/state"
)

type Service struct {
	LoadApp    func(http.ResponseWriter, *http.Request, state.Account, string) (state.App, bool)
	Store      deploymentcheck.Store
	GatewayURL string
}

func (s Service) Check(w http.ResponseWriter, r *http.Request, account state.Account) {
	w.Header().Set("Cache-Control", "no-store")
	app, ok := s.LoadApp(w, r, account, r.PathValue("slug"))
	if !ok {
		return
	}
	var request api.DeploymentResponseCheckRequest
	if !httpkit.DecodeJSONLimit(w, r, &request, api.DeploymentResponseCheckMaxWireBytes) {
		return
	}
	deploymentID := r.PathValue("deployment")
	if r.URL.RawQuery != "" || request.Validate() != nil || !api.ValidDeploymentResponseCheckID(deploymentID) {
		api.WriteProblem(w, api.ErrValidation("a valid deployment, pinned identities, explicit scope, and app-relative GET path are required; query parameters are not supported"))
		return
	}
	if request.AppID != app.ID || app.AccountID != account.ID {
		api.WriteProblem(w, api.NewProblem(http.StatusNotFound, api.CodeNotFound, "App not found", "the app does not match the selected identity"))
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), api.DeploymentResponseCheckTimeout)
	defer cancel()
	if !s.checkTarget(w, ctx, request, deploymentID) {
		return
	}
	result, err := s.forward(ctx, app.Slug, deploymentID, request)
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("deployment response check is unavailable; retry the same deployment"))
		return
	}
	if !s.checkTarget(w, ctx, request, deploymentID) {
		return
	}
	httpkit.WriteJSON(w, http.StatusOK, result)
}

func (s Service) checkTarget(w http.ResponseWriter, ctx context.Context, request api.DeploymentResponseCheckRequest, deploymentID string) bool {
	_, _, err := deploymentcheck.Resolve(ctx, s.Store, request, deploymentID)
	if err == nil {
		return true
	}
	if errors.Is(err, state.ErrNotFound) || errors.Is(err, state.ErrConflict) {
		api.WriteProblem(w, api.NewProblem(http.StatusConflict, "deployment_check_target_changed", "Deployment target unavailable", "the deployment or its environment no longer matches the selected target"))
	} else {
		api.WriteProblem(w, api.ErrCapacity("could not verify deployment response-check identity"))
	}
	return false
}

func (s Service) forward(ctx context.Context, slug, deploymentID string, request api.DeploymentResponseCheckRequest) (api.DeploymentResponseCheckResult, error) {
	var result api.DeploymentResponseCheckResult
	if s.GatewayURL == "" {
		return result, errors.New("gateway not configured")
	}
	endpoint, err := url.JoinPath(s.GatewayURL, "v1", "internal", "apps", slug, "response-check")
	if err != nil {
		return result, err
	}
	query := url.Values{"deployment_id": []string{deploymentID}}
	body, err := json.Marshal(request)
	if err != nil {
		return result, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint+"?"+query.Encode(), bytes.NewReader(body))
	if err != nil {
		return result, err
	}
	req.Header.Set("Content-Type", "application/json")
	client := &http.Client{Timeout: api.DeploymentResponseCheckTimeout, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	response, err := client.Do(req)
	if err != nil {
		return result, err
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		return result, errors.New("gateway check unavailable")
	}
	wire, err := io.ReadAll(io.LimitReader(response.Body, api.DeploymentResponseCheckMaxWireBytes+1))
	if err != nil || len(wire) > api.DeploymentResponseCheckMaxWireBytes {
		return result, errors.New("gateway response exceeded its limit")
	}
	if json.Unmarshal(wire, &result) != nil || !result.Matches(request, deploymentID) {
		return result, errors.New("gateway response identity or evidence was invalid")
	}
	return result, nil
}
