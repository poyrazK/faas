// Package mcpcontract serves the owner side of the tool contract gate for
// sold MCP servers (ADR-20261127191645): the contract the next production
// candidate is compared with, recent checks, and the audited overrides that
// let one breaking candidate ship. imaged runs the gate; pkg/apid/apidserver
// only wires the routes.
package mcpcontract

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/httpkit"
	"github.com/onebox-faas/faas/pkg/apid/mcplisting"
	"github.com/onebox-faas/faas/pkg/mcphosting"
	"github.com/onebox-faas/faas/pkg/state"
)

// DocsURL documents the gate.
const DocsURL = "https://gregale.dev/docs/api-monetization#mcp-tool-contract"

// Host is what the owner routes need from apid.
type Host interface {
	Store() state.Store
	// AppRoute resolves the route's app; on false it has written the problem.
	AppRoute(w http.ResponseWriter, r *http.Request, acct state.Account) (state.App, bool)
	Audit(ctx context.Context, event, accountID string, fields map[string]any)
	// Actor names who made the request, for the override's created_by.
	Actor(r *http.Request, acct state.Account) string
}

// Owner serves GET /v1/apps/{slug}/mcp-contract and
// POST /v1/apps/{slug}/mcp-contract/overrides.
type Owner struct {
	Host Host
	Log  *slog.Logger
	// Now is the clock; nil is time.Now.
	Now func() time.Time
}

func (o Owner) now() time.Time {
	if o.Now != nil {
		return o.Now()
	}
	return time.Now()
}

func (o Owner) route(w http.ResponseWriter, r *http.Request, acct state.Account) (state.MCPContractStore, state.App, bool) {
	store, ok := o.Host.Store().(state.MCPContractStore)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("MCP tool contracts"))
		return nil, state.App{}, false
	}
	app, ok := o.Host.AppRoute(w, r, acct)
	return store, app, ok
}

func notGated(w http.ResponseWriter) {
	api.WriteProblem(w, api.NewProblem(http.StatusNotFound, api.CodeNotFound, "No tool contract",
		"this app has no MCP listing published with a tool contract; publish one with gregale mcp server listing publish").WithDocs(DocsURL))
}

// Get returns the baseline, the newest checks and the open overrides.
func (o Owner) Get(w http.ResponseWriter, r *http.Request, acct state.Account) {
	store, app, ok := o.route(w, r, acct)
	if !ok {
		return
	}
	baseline, err := store.MCPContractBaseline(r.Context(), app.ID)
	if errors.Is(err, state.ErrNotFound) || err == nil && baseline.AccountID != app.AccountID {
		notGated(w)
		return
	}
	if err != nil {
		o.Log.Error("read MCP contract baseline", "app_id", app.ID, "err", err)
		api.WriteProblem(w, api.ErrInternal("could not read the MCP tool contract"))
		return
	}
	checks, err := store.ListMCPContractChecks(r.Context(), app.AccountID, app.ID, api.MCPContractChecksMax)
	if err != nil {
		o.Log.Error("list MCP contract checks", "app_id", app.ID, "err", err)
		api.WriteProblem(w, api.ErrInternal("could not read the MCP tool contract checks"))
		return
	}
	overrides, err := store.ListOpenMCPContractOverrides(r.Context(), app.AccountID, app.ID)
	if err != nil {
		o.Log.Error("list MCP contract overrides", "app_id", app.ID, "err", err)
		api.WriteProblem(w, api.ErrInternal("could not read the MCP tool contract overrides"))
		return
	}
	httpkit.WriteJSON(w, http.StatusOK, Response(app.Slug, baseline, checks, overrides))
}

// CreateOverride records an audited, one-shot permission for the app's next
// breaking or unverified production candidate.
func (o Owner) CreateOverride(w http.ResponseWriter, r *http.Request, acct state.Account) {
	store, app, ok := o.route(w, r, acct)
	if !ok {
		return
	}
	var req api.CreateMCPContractOverrideRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, api.MCPContractOverrideRequestMaxBytes)).Decode(&req); err != nil {
		api.WriteProblem(w, api.ErrValidation("invalid JSON body: "+err.Error()))
		return
	}
	reason, ttl, problem := ValidateOverride(req)
	if problem != nil {
		api.WriteProblem(w, problem)
		return
	}
	baseline, err := store.MCPContractBaseline(r.Context(), app.ID)
	if errors.Is(err, state.ErrNotFound) || err == nil && baseline.AccountID != app.AccountID {
		notGated(w)
		return
	}
	if err != nil {
		o.Log.Error("read MCP contract baseline", "app_id", app.ID, "err", err)
		api.WriteProblem(w, api.ErrInternal("could not read the MCP tool contract"))
		return
	}
	if problem := o.checkDeployment(r.Context(), app, req.DeploymentID); problem != nil {
		api.WriteProblem(w, problem)
		return
	}
	created, err := store.CreateMCPContractOverride(r.Context(), state.MCPContractOverride{AppID: app.ID, AccountID: app.AccountID,
		Reason: reason, CreatedBy: o.Host.Actor(r, acct), ExpiresAt: o.now().Add(ttl), DeploymentID: req.DeploymentID, Kind: overrideKind(req.Kind)},
		api.MCPContractOverrideOpenCap)
	if errors.Is(err, state.ErrMCPContractOverrideCap) {
		api.WriteProblem(w, api.NewProblem(http.StatusConflict, api.CodeConflict, "Too many open overrides",
			fmt.Sprintf("this app already has %d open overrides; each admits one candidate, so deploy or let them expire", api.MCPContractOverrideOpenCap)).
			WithLimit(api.MCPContractOverrideOpenCap, api.MCPContractOverrideOpenCap).WithDocs(DocsURL))
		return
	}
	if err != nil {
		o.Log.Error("create MCP contract override", "app_id", app.ID, "err", err)
		api.WriteProblem(w, api.ErrInternal("could not record the override"))
		return
	}
	o.Host.Audit(r.Context(), "mcp_contract.override_created", acct.ID, map[string]any{"app_id": app.ID, "override_id": created.ID,
		"reason": created.Reason, "expires_at": created.ExpiresAt, "deployment_id": created.DeploymentID, "kind": created.Kind})
	httpkit.WriteJSON(w, http.StatusCreated, Override(created))
}

// checkDeployment requires a bound override's deployment to be one of the
// app's own.
func (o Owner) checkDeployment(ctx context.Context, app state.App, id string) *api.Problem {
	if id == "" {
		return nil
	}
	if _, err := uuid.Parse(id); err != nil {
		return invalid("deployment_id must be a deployment ID")
	}
	dep, err := o.Host.Store().DeploymentByID(ctx, id)
	if errors.Is(err, state.ErrNotFound) || err == nil && dep.AppID != app.ID {
		return api.NewProblem(http.StatusNotFound, api.CodeNotFound, "Deployment not found",
			"deployment_id names no deployment of this app").WithDocs(DocsURL)
	}
	if err != nil {
		o.Log.Error("read deployment for MCP contract override", "app_id", app.ID, "err", err)
		return api.ErrInternal("could not read the deployment")
	}
	return nil
}

// ValidateOverride trims the reason and resolves the lifetime.
func ValidateOverride(req api.CreateMCPContractOverrideRequest) (string, time.Duration, *api.Problem) {
	reason := strings.TrimSpace(req.Reason)
	if reason == "" {
		return "", 0, invalid("reason is required: say why paying customers' tools may break")
	}
	if !state.ValidMCPContractOverrideKind(overrideKind(req.Kind)) {
		return "", 0, invalid("kind must be contract or tool_scan")
	}
	if n := len(reason); n > api.MCPContractReasonMax || !utf8.ValidString(reason) {
		return "", 0, invalid(fmt.Sprintf("reason must be valid UTF-8 of at most %d bytes", api.MCPContractReasonMax)).
			WithLimit(api.MCPContractReasonMax, int64(n))
	}
	ttl := api.MCPContractOverrideDefaultTTL
	if req.ExpiresInSeconds != 0 {
		ttl = time.Duration(req.ExpiresInSeconds) * time.Second
	}
	if ttl < api.MCPContractOverrideMinTTL || ttl > api.MCPContractOverrideMaxTTL {
		return "", 0, invalid(fmt.Sprintf("expires_in_seconds must be between %d and %d",
			int(api.MCPContractOverrideMinTTL.Seconds()), int(api.MCPContractOverrideMaxTTL.Seconds()))).
			WithLimit(int64(api.MCPContractOverrideMaxTTL.Seconds()), int64(req.ExpiresInSeconds))
	}
	return reason, ttl, nil
}

// overrideKind defaults an empty kind to contract.
func overrideKind(kind string) string {
	if kind == "" {
		return state.MCPContractOverrideKindContract
	}
	return kind
}

func invalid(detail string) *api.Problem {
	return api.NewProblem(http.StatusUnprocessableEntity, api.CodeValidation, "Invalid MCP contract override", detail).WithDocs(DocsURL)
}

// Response is the owner's view of the contract.
func Response(slug string, b state.MCPContractBaseline, checks []state.MCPContractCheck, overrides []state.MCPContractOverride) api.MCPContractResponse {
	out := api.MCPContractResponse{App: slug, BaselineSHA256: b.SHA256, BaselineDeploymentID: b.FromDeployment, ListingVersion: b.ListingVersion,
		Tools: contractTools(b.Contract), ProbeSecret: b.ProbeSecret, Checks: make([]api.MCPContractCheck, 0, len(checks)),
		BaselineToolScan: mcplisting.ToolScanView(b.ToolScan, b.ToolScanAt, ""),
		Overrides:        make([]api.MCPContractOverride, 0, len(overrides))}
	for _, c := range checks {
		out.Checks = append(out.Checks, Check(c))
	}
	for _, ov := range overrides {
		out.Overrides = append(out.Overrides, Override(ov))
	}
	return out
}

// Check converts a stored check to the wire form.
func Check(c state.MCPContractCheck) api.MCPContractCheck {
	changes := []api.MCPContractChange{}
	_ = json.Unmarshal(c.Changes, &changes)
	checked := c.CheckedAt
	return api.MCPContractCheck{DeploymentID: c.DeploymentID, Verdict: c.Verdict, BaselineSHA256: c.BaselineSHA256,
		ContractSHA256: c.CandidateSHA256, Overridden: c.OverrideID != "", Changes: changes, CheckedAt: c.CheckedAt, AcceptedAt: c.AcceptedAt,
		ToolScan: mcplisting.ToolScanView(c.ToolScan, &checked, ""), ToolScanNewHigh: c.ToolScanNewHigh,
		ToolScanOverridden: c.ToolScanOverrideID != ""}
}

// Override converts a stored override to the wire form.
func Override(o state.MCPContractOverride) api.MCPContractOverride {
	return api.MCPContractOverride{ID: o.ID, Kind: overrideKind(o.Kind), Reason: o.Reason, CreatedBy: o.CreatedBy, CreatedAt: o.CreatedAt, ExpiresAt: o.ExpiresAt,
		DeploymentID: o.DeploymentID}
}

// Changelog is the public listing's view of accepted changes: dates,
// verdicts and changed tool names, never schemas or override reasons.
func Changelog(checks []state.MCPContractCheck) []api.MCPContractChangelogEntry {
	out := make([]api.MCPContractChangelogEntry, 0, len(checks))
	for _, c := range checks {
		if c.AcceptedAt == nil {
			continue
		}
		var changes []api.MCPContractChange
		_ = json.Unmarshal(c.Changes, &changes)
		tools := []string{}
		for _, ch := range changes {
			if ch.Tool != "" && !slices.Contains(tools, ch.Tool) {
				tools = append(tools, ch.Tool)
			}
		}
		slices.Sort(tools)
		out = append(out, api.MCPContractChangelogEntry{Date: *c.AcceptedAt, Verdict: c.Verdict, Tools: tools, Changes: len(changes)})
	}
	return out
}

func contractTools(raw json.RawMessage) []string {
	names := []string{}
	contract, err := mcphosting.ReadContract(bytes.NewReader(raw))
	if err != nil {
		return names
	}
	for _, t := range contract.Tools {
		names = append(names, t.Name)
	}
	slices.Sort(names)
	return names
}
