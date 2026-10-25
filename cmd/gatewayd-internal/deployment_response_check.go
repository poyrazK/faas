package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"unicode/utf8"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/deploymentcheck"
	"github.com/onebox-faas/faas/pkg/gateway"
	"github.com/onebox-faas/faas/pkg/reqbudget"
	"github.com/onebox-faas/faas/pkg/safetext"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/wire"
)

type deploymentResponsePicker interface {
	PickForDeployment(string, string) gateway.PickResult
}

const deploymentCheckUnavailable = "deployment_check_unavailable"

type deploymentResponseChecker struct {
	store      state.Store
	picker     deploymentResponsePicker
	ensure     func(context.Context, gateway.App, string) error
	forward    func(gateway.Target) http.Handler
	appsDomain string
}

type deploymentResponseTarget struct {
	gateway.Target
	protocol string
}

// This handler is registered only on the trusted control listener. It creates
// a fresh GET with no caller credentials or workload-supplied routing headers.
func (c deploymentResponseChecker) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	slug, deploymentID, request, err := decodeDeploymentResponseCheck(w, r)
	if err != nil {
		c.problem(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	if !c.configured() {
		c.problem(w, http.StatusServiceUnavailable, deploymentCheckUnavailable, "deployment response check is not configured")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), api.DeploymentResponseCheckTimeout)
	defer cancel()
	target, err := c.target(ctx, slug, deploymentID, request)
	if err != nil {
		c.targetProblem(w, err)
		return
	}
	result, err := c.check(ctx, target, slug, request)
	if err != nil {
		c.problem(w, http.StatusServiceUnavailable, deploymentCheckUnavailable, "the selected deployment did not return a response")
		return
	}
	if err := c.validateTarget(ctx, target.Target, request); err != nil {
		c.targetProblem(w, err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(result)
}

func decodeDeploymentResponseCheck(w http.ResponseWriter, r *http.Request) (string, string, api.DeploymentResponseCheckRequest, error) {
	var request api.DeploymentResponseCheckRequest
	const prefix, suffix = "/v1/internal/apps/", "/response-check"
	slug := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, prefix), suffix)
	query := r.URL.Query()
	deploymentID := query.Get("deployment_id")
	if r.Method != http.MethodPost || !strings.HasPrefix(r.URL.Path, prefix) || !strings.HasSuffix(r.URL.Path, suffix) || !api.ValidAppSlug(slug) || len(query) != 1 || len(query["deployment_id"]) != 1 || !api.ValidDeploymentResponseCheckID(deploymentID) {
		return "", "", request, errors.New("a valid app and one deployment_id are required")
	}
	r.Body = http.MaxBytesReader(w, r.Body, api.DeploymentResponseCheckMaxWireBytes)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if decoder.Decode(&request) != nil || request.Validate() != nil {
		return "", "", request, errors.New("invalid deployment response-check request")
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return "", "", request, errors.New("one bounded request object is required")
	}
	return slug, deploymentID, request, nil
}

func (c deploymentResponseChecker) target(ctx context.Context, slug, deploymentID string, request api.DeploymentResponseCheckRequest) (deploymentResponseTarget, error) {
	app, dep, err := deploymentcheck.Resolve(ctx, c.store, request, deploymentID)
	if err != nil {
		return deploymentResponseTarget{}, err
	}
	if app.Slug != slug {
		return deploymentResponseTarget{}, state.ErrNotFound
	}
	resolved, found, err := (pgRouter{store: c.store}).toAppWithDeployment(ctx, app, &dep)
	if err != nil {
		return deploymentResponseTarget{}, err
	}
	if !found || resolved.SecurityQuarantined || resolved.EnvironmentNotReady || resolved.AccountAbuseHeld || resolved.AccountStatus == "suspended" || resolved.AccountStatus == "deleted_pending" {
		return deploymentResponseTarget{}, state.ErrConflict
	}
	resolved.Scope = request.Scope
	if err := c.ensure(ctx, resolved, deploymentID); err != nil {
		return deploymentResponseTarget{}, err
	}
	pick := c.picker.PickForDeployment(app.ID, deploymentID)
	if !pick.OK || pick.Target.AppID != app.ID || pick.Target.DeploymentID != deploymentID {
		return deploymentResponseTarget{}, state.ErrConflict
	}
	if err := c.validateTarget(ctx, pick.Target, request); err != nil {
		return deploymentResponseTarget{}, err
	}
	return deploymentResponseTarget{Target: pick.Target, protocol: resolved.AppProtocol}, nil
}

func (c deploymentResponseChecker) validateTarget(ctx context.Context, target gateway.Target, request api.DeploymentResponseCheckRequest) error {
	if _, _, err := deploymentcheck.Resolve(ctx, c.store, request, target.DeploymentID); err != nil {
		return err
	}
	instance, err := c.store.InstanceByID(ctx, target.InstanceID)
	if err != nil {
		return err
	}
	if instance.AppID != request.AppID || instance.DeploymentID != target.DeploymentID || instance.State != string(state.StateRunning) || instance.NodeID == "" || instance.NodeID != target.NodeID || instance.WakeID != target.WakeID {
		return state.ErrConflict
	}
	return nil
}

func (c deploymentResponseChecker) check(ctx context.Context, target deploymentResponseTarget, slug string, request api.DeploymentResponseCheckRequest) (api.DeploymentResponseCheckResult, error) {
	result := api.DeploymentResponseCheckResult{AppID: request.AppID, DeploymentID: target.DeploymentID, InstanceID: target.InstanceID, Scope: request.Scope, ProjectID: request.ProjectID, EnvironmentID: request.EnvironmentID}
	path, _ := url.Parse(request.Path)
	path.Scheme, path.Host = "http", slug
	if request.EnvironmentID != "" {
		path.Host = gateway.BuildEnvironmentHost(wire.DeployWildcardSuffix, request.EnvironmentID, request.AppID)
	} else if domain := strings.TrimPrefix(c.appsDomain, "."); domain != "" {
		path.Host += "." + domain
	}
	// Keep the hard check deadline and truncation cancellation even when the
	// streaming bridge detaches an ordinary response budget after headers.
	probeCtx, cancel := context.WithCancel(reqbudget.WithoutBudget(ctx))
	defer cancel()
	probe, err := http.NewRequestWithContext(probeCtx, http.MethodGet, path.String(), nil)
	if err != nil {
		return result, err
	}
	probe.Header.Set("Cache-Control", "no-store")
	api.PlatformIdentity{AppID: target.AppID, DeploymentID: target.DeploymentID, InstanceID: target.InstanceID, NodeID: target.NodeID}.ApplyGuestHeaders(probe.Header)
	probe.Header.Set("x-faas-wake-id", target.WakeID)
	probe.Header.Set("x-faas-protocol", target.protocol)
	response := &boundedMCPProbeResponseWriter{header: make(http.Header), cancel: cancel, maxBytes: api.DeploymentResponseCheckMaxBodyBytes}
	if serveBoundedProbe(c.forward(target.Target), response, probe) || ctx.Err() != nil || response.status < 200 || response.status > 599 {
		return result, errors.New("no complete HTTP response")
	}
	result.HTTPStatus, result.BodyTruncated = response.status, response.tooLarge
	// JSON text is UTF-8; invalid or partial byte sequences cannot expand the
	// decoded preview beyond the response bound or pretend to be exact text.
	text := strings.ToValidUTF8(string(response.body), "\uFFFD")
	result.Body = safetext.Truncate(text, api.DeploymentResponseCheckMaxBodyBytes)
	result.BodyTruncated = result.BodyTruncated || !utf8.Valid(response.body) || len(text) > len(result.Body)
	return result, nil
}

func (c deploymentResponseChecker) problem(w http.ResponseWriter, status int, code, detail string) {
	api.WriteProblem(w, api.NewProblem(status, code, "Deployment response check failed", detail))
}

func (c deploymentResponseChecker) targetProblem(w http.ResponseWriter, err error) {
	if errors.Is(err, state.ErrNotFound) || errors.Is(err, state.ErrConflict) {
		c.problem(w, http.StatusConflict, "deployment_check_target_changed", "the selected deployment, environment, or running replica is unavailable")
	} else {
		c.problem(w, http.StatusServiceUnavailable, deploymentCheckUnavailable, "the selected deployment could not be checked")
	}
}
