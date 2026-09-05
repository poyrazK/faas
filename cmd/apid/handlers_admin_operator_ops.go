package main

// Operator workload and node operations (issue #777 follow-up).
//
// The original observability endpoints answered "how many tenants/nodes do I
// have?". These bounded drill-downs answer the operational questions that
// follow: what did one customer do, which deployments/instances belong to an
// app, and what is currently placed on a compute node. Node mutations reuse
// the existing state.Store ownership boundary and emit operator.action.*
// audit rows so the web console does not need direct database access.

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/appmetrics"
	"github.com/onebox-faas/faas/pkg/state"
)

const (
	obsOpsActivityLimitDefault = 50
	obsOpsActivityLimitMax     = 200
	obsOpsReasonMaxLen         = 64
)

var obsOpsReasonShape = regexp.MustCompile(`^[a-z0-9_]+$`)

func (s *server) obsTenantActivity(w http.ResponseWriter, r *http.Request, acct state.Account) {
	if allowed, prob := s.adminAllows(acct); !allowed {
		api.WriteProblem(w, prob)
		return
	}
	targetID := r.PathValue("id")
	if _, err := uuid.Parse(targetID); err != nil {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation, "Bad account id", "expected UUID"))
		return
	}
	if _, err := s.store.AccountByID(r.Context(), targetID); err != nil {
		api.WriteProblem(w, api.NewProblem(http.StatusNotFound, api.CodeNotFound, "Account not found", err.Error()))
		return
	}
	prob, limit := api.ParseLimit(r.URL.Query().Get("limit"), obsOpsActivityLimitDefault, obsOpsActivityLimitMax, "activity")
	if prob != nil {
		api.WriteProblem(w, prob)
		return
	}
	invocations, err := s.store.ListInvocationsForAccount(r.Context(), targetID, limit, "")
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not list tenant invocations"))
		return
	}
	auditAccountID, _ := uuid.Parse(targetID)
	auditEvents, err := s.store.ListAuditLog(r.Context(), state.AuditLogFilter{
		AccountID: &auditAccountID,
		Limit:     limit,
	})
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not list tenant audit activity"))
		return
	}
	apps, err := s.store.ListApps(r.Context(), targetID)
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not list tenant apps"))
		return
	}
	appSlugs := make(map[string]string, len(apps))
	for _, app := range apps {
		appSlugs[app.ID] = app.Slug
	}
	writeJSON(w, http.StatusOK, api.ObsTenantActivityResponse{
		AccountID:   targetID,
		GeneratedAt: time.Now().UTC(),
		Invocations: projectObsInvocations(invocations, appSlugs, limit),
		AuditEvents: projectObsAuditActivity(auditEvents),
		Limit:       limit,
	})
}

func (s *server) postObsAccountSuspend(w http.ResponseWriter, r *http.Request, acct state.Account) {
	s.postObsAccountMutation(w, r, acct, "suspend")
}

func (s *server) postObsAccountRestore(w http.ResponseWriter, r *http.Request, acct state.Account) {
	s.postObsAccountMutation(w, r, acct, "restore")
}

func (s *server) postObsAccountRevokeSessions(w http.ResponseWriter, r *http.Request, acct state.Account) {
	s.postObsAccountMutation(w, r, acct, "revoke-sessions")
}

func (s *server) postObsAccountMutation(w http.ResponseWriter, r *http.Request, acct state.Account, action string) {
	if allowed, prob := s.adminAllows(acct); !allowed {
		api.WriteProblem(w, prob)
		return
	}
	if r.URL.Query().Get("confirm") != "true" {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation, "confirm required", "?confirm=true is required for account lifecycle changes"))
		return
	}
	reason := r.URL.Query().Get("reason")
	if reason == "" {
		reason = "operator_" + strings.ReplaceAll(action, "-", "_")
	}
	if len(reason) > obsOpsReasonMaxLen || !obsOpsReasonShape.MatchString(reason) {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation, "invalid reason", "reason must match [a-z0-9_]{1,64}"))
		return
	}
	targetID := r.PathValue("id")
	if _, err := uuid.Parse(targetID); err != nil {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation, "Bad account id", "expected UUID"))
		return
	}
	if targetID == acct.ID {
		api.WriteProblem(w, api.NewProblem(http.StatusConflict, api.CodeConflict, "cannot modify operator account", "use a separate operator account for lifecycle actions"))
		return
	}
	if _, err := s.store.AccountByID(r.Context(), targetID); err != nil {
		api.WriteProblem(w, api.NewProblem(http.StatusNotFound, api.CodeNotFound, "Account not found", err.Error()))
		return
	}
	revokedSessions := 0
	var err error
	switch action {
	case "suspend":
		err = s.store.UpdateAccountStatus(r.Context(), targetID, state.AccountSuspended)
		if err == nil {
			// uuid.Nil is an impossible session id and therefore makes the
			// operator action revoke every active session for the target.
			revokedSessions, err = s.store.RevokeAllSessions(r.Context(), targetID, uuid.Nil.String())
		}
	case "restore":
		err = s.store.UpdateAccountStatus(r.Context(), targetID, state.AccountActive)
	case "revoke-sessions":
		revokedSessions, err = s.store.RevokeAllSessions(r.Context(), targetID, uuid.Nil.String())
	default:
		err = fmt.Errorf("unsupported account action %q", action)
	}
	if err != nil {
		if errors.Is(err, state.ErrNotFound) {
			api.WriteProblem(w, api.NewProblem(http.StatusNotFound, api.CodeNotFound, "Account not found", err.Error()))
			return
		}
		api.WriteProblem(w, api.ErrCapacity("could not apply account operation"))
		return
	}
	target, err := s.store.AccountByID(r.Context(), targetID)
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not reload account after operation"))
		return
	}
	if s.audit != nil {
		subject := targetID
		s.audit.Emit(r.Context(), "operator.action.account_"+strings.ReplaceAll(action, "-", "_"), &subject, map[string]any{
			"actor":             acct.ID,
			"target_account_id": targetID,
			"revoked_sessions":  revokedSessions,
			"reason":            reason,
		})
	}
	writeJSON(w, http.StatusOK, api.ObsAccountMutationResponse{
		Account:         projectTenantRow(r.Context(), s.store, target, false),
		Action:          action,
		RevokedSessions: revokedSessions,
	})
}

func (s *server) obsAppDetail(w http.ResponseWriter, r *http.Request, acct state.Account) {
	if allowed, prob := s.adminAllows(acct); !allowed {
		api.WriteProblem(w, prob)
		return
	}
	appID := r.PathValue("id")
	if _, err := uuid.Parse(appID); err != nil {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation, "Bad app id", "expected UUID"))
		return
	}
	app, err := s.store.AppByID(r.Context(), appID)
	if err != nil {
		status := http.StatusInternalServerError
		code := api.CodeInternal
		if errors.Is(err, state.ErrNotFound) {
			status = http.StatusNotFound
			code = api.CodeNotFound
		}
		api.WriteProblem(w, api.NewProblem(status, code, "App not found", err.Error()))
		return
	}
	deployments, err := s.store.ListDeploymentsForApp(r.Context(), app.ID, 100, 0)
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not list app deployments"))
		return
	}
	instances, err := s.store.ListInstancesForApp(r.Context(), app.ID)
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not list app instances"))
		return
	}
	invocations, err := s.store.ListInvocationsForApp(r.Context(), app.ID)
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not list app invocations"))
		return
	}
	if len(invocations) > obsOpsActivityLimitMax {
		invocations = invocations[:obsOpsActivityLimitMax]
	}
	rng := r.URL.Query().Get("range")
	if rng == "" {
		rng = appmetrics.DefaultRange
	}
	if !appmetrics.IsValidRange(rng) {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation, "invalid range", fmt.Sprintf("range must be one of: %s", strings.Join(appmetrics.Ranges(), ", "))))
		return
	}
	health, err := s.obsAppHealth(r.Context(), app, rng)
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not load app health"))
		return
	}
	writeJSON(w, http.StatusOK, api.ObsAppDetailResponse{
		App: api.ObsAppDetail{
			ID:             app.ID,
			AccountID:      app.AccountID,
			Slug:           app.Slug,
			Type:           string(app.Type),
			Runtime:        app.Runtime,
			Status:         string(app.Status),
			RAMMB:          app.RAMMB,
			MaxConcurrency: app.MaxConcurrency,
			MinInstances:   app.MinInstances,
			CreatedAt:      app.CreatedAt,
		},
		Deployments: projectObsDeployments(deployments),
		Instances:   projectObsInstances(instances, map[string]string{app.ID: app.Slug}, map[string]string{app.ID: app.AccountID}, s.nodeNames(r.Context())),
		Invocations: projectObsInvocations(invocations, map[string]string{app.ID: app.Slug}, obsOpsActivityLimitMax),
		Health:      health,
	})
}

func (s *server) obsAppHealth(ctx context.Context, app state.App, rng string) (api.ObsAppHealth, error) {
	now := time.Now().UTC()
	metrics, source := appmetrics.Fetch(ctx, s.promqlClient, s.log, app.ID, rng)
	metrics.AppID = app.ID
	metrics.Range = rng
	metrics.Source = source
	metrics.AsOf = now.Format(time.RFC3339Nano)
	since := now.Add(-24 * time.Hour)
	rows, err := s.store.ListAppErrorGroups(ctx, buildAppErrorsSummaryParams(
		app.AccountID, app.ID, since, now, nil, nil, nil, 10,
	))
	if err != nil {
		return api.ObsAppHealth{}, err
	}
	return api.ObsAppHealth{
		GeneratedAt:       now,
		Metrics:           metrics,
		Errors:            projectAppErrorSummaryRows(rows),
		ErrorsWindowStart: since,
		ErrorsWindowEnd:   now,
	}, nil
}

func (s *server) obsNodeDetail(w http.ResponseWriter, r *http.Request, acct state.Account) {
	if allowed, prob := s.adminAllows(acct); !allowed {
		api.WriteProblem(w, prob)
		return
	}
	node, err := s.store.ComputeNodeByName(r.Context(), r.PathValue("name"))
	if err != nil {
		status := http.StatusInternalServerError
		code := api.CodeInternal
		if errors.Is(err, state.ErrNotFound) {
			status = http.StatusNotFound
			code = api.CodeNotFound
		}
		api.WriteProblem(w, api.NewProblem(status, code, "Node not found", err.Error()))
		return
	}
	apps, err := s.store.ListAppsByNodeID(r.Context(), node.ID)
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not list node apps"))
		return
	}
	instances, err := s.store.ListInstancesOnNodeID(r.Context(), node.ID)
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not list node instances"))
		return
	}
	appByID := make(map[string]state.App, len(apps))
	for _, app := range apps {
		appByID[app.ID] = app
	}
	// Apps are normally owned by the node, but a live migration can leave
	// the physical instance on this node before scheduler ownership moves.
	// Fill in metadata for those rows so the node detail remains useful.
	for _, instance := range instances {
		if _, ok := appByID[instance.AppID]; ok {
			continue
		}
		app, appErr := s.store.AppByID(r.Context(), instance.AppID)
		if appErr == nil {
			apps = append(apps, app)
			appByID[app.ID] = app
		}
	}
	live, err := s.store.PerNodeLiveStats(r.Context())
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not aggregate node live stats"))
		return
	}
	heartbeats, err := s.store.LatestHeartbeatStats(r.Context())
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not aggregate node heartbeat stats"))
		return
	}
	rows, _ := paginateNodes([]state.ComputeNode{node}, 1, live, heartbeats)
	writeJSON(w, http.StatusOK, api.ObsNodeDetailResponse{
		Node:      rows[0],
		Apps:      projectObsNodeApps(apps, instances),
		Instances: projectObsInstances(instances, appSlugs(appByID), appAccounts(appByID), map[string]string{node.ID: node.Name}),
		Drain:     projectObsDrainStatus(instances),
	})
}

func (s *server) postObsNodeDrain(w http.ResponseWriter, r *http.Request, acct state.Account) {
	s.postObsNodeMutation(w, r, acct, "drain", false)
}

func (s *server) postObsNodeForceDrain(w http.ResponseWriter, r *http.Request, acct state.Account) {
	s.postObsNodeMutation(w, r, acct, "force-drain", true)
}

func (s *server) postObsNodeActivate(w http.ResponseWriter, r *http.Request, acct state.Account) {
	s.postObsNodeMutation(w, r, acct, "activate", false)
}

func (s *server) postObsNodeMutation(w http.ResponseWriter, r *http.Request, acct state.Account, action string, forced bool) {
	if allowed, prob := s.adminAllows(acct); !allowed {
		api.WriteProblem(w, prob)
		return
	}
	if r.URL.Query().Get("confirm") != "true" {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation, "confirm required", "?confirm=true is required for compute-node state changes"))
		return
	}
	reason := r.URL.Query().Get("reason")
	if reason == "" {
		reason = "operator_" + strings.ReplaceAll(action, "-", "_")
	}
	if len(reason) > obsOpsReasonMaxLen || !obsOpsReasonShape.MatchString(reason) {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation, "invalid reason", "reason must match [a-z0-9_]{1,64}"))
		return
	}
	node, err := s.store.ComputeNodeByName(r.Context(), r.PathValue("name"))
	if err != nil {
		api.WriteProblem(w, api.NewProblem(http.StatusNotFound, api.CodeNotFound, "Node not found", err.Error()))
		return
	}
	instances, err := s.store.ListInstancesOnNodeID(r.Context(), node.ID)
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not inspect node instances"))
		return
	}
	liveInstances := countObsLiveInstances(instances)
	previousActive := node.Active
	if action == "activate" {
		err = s.store.SetComputeNodeActive(r.Context(), node.ID, true)
	} else {
		err = s.store.MarkComputeNodeInactive(r.Context(), node.ID)
	}
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not change compute-node state"))
		return
	}
	if s.audit != nil {
		subject := node.ID
		s.audit.Emit(r.Context(), "operator.action.node_"+strings.ReplaceAll(action, "-", "_"), &subject, map[string]any{
			"actor":           acct.ID,
			"node_id":         node.ID,
			"node_name":       node.Name,
			"previous_active": previousActive,
			"active":          action == "activate",
			"live_instances":  liveInstances,
			"forced":          forced,
			"reason":          reason,
		})
	}
	writeJSON(w, http.StatusOK, api.ObsNodeMutationResponse{
		OK:             true,
		Node:           node.Name,
		PreviousActive: previousActive,
		Active:         action == "activate",
		LiveInstances:  liveInstances,
		Forced:         forced,
		Reason:         reason,
	})
}

func (s *server) nodeNames(ctx context.Context) map[string]string {
	rows, err := s.store.ListComputeNodes(ctx, true)
	if err != nil {
		return map[string]string{}
	}
	out := make(map[string]string, len(rows))
	for _, node := range rows {
		out[node.ID] = node.Name
	}
	return out
}

func appSlugs(apps map[string]state.App) map[string]string {
	out := make(map[string]string, len(apps))
	for id, app := range apps {
		out[id] = app.Slug
	}
	return out
}

func appAccounts(apps map[string]state.App) map[string]string {
	out := make(map[string]string, len(apps))
	for id, app := range apps {
		out[id] = app.AccountID
	}
	return out
}

func projectObsInvocations(rows []state.Invocation, appSlugs map[string]string, limit int) []api.ObsInvocationRow {
	if len(rows) > limit {
		rows = rows[:limit]
	}
	out := make([]api.ObsInvocationRow, 0, len(rows))
	for _, row := range rows {
		item := api.ObsInvocationRow{
			ID:          row.ID,
			AppID:       row.AppID,
			AppSlug:     appSlugs[row.AppID],
			State:       string(row.State),
			Source:      string(row.Source),
			Method:      row.Method,
			Path:        row.Path,
			Attempts:    row.Attempts,
			LastError:   truncateObsText(row.LastError, 512),
			CreatedAt:   row.CreatedAt,
			CompletedAt: row.CompletedAt,
		}
		if row.Outcome != nil {
			item.Outcome = string(*row.Outcome)
		}
		out = append(out, item)
	}
	return out
}

func projectObsAuditActivity(rows []state.AuditLog) []api.ObsAuditActivityRow {
	out := make([]api.ObsAuditActivityRow, 0, len(rows))
	for _, row := range rows {
		actor := row.Actor
		// AccountEmail is intentionally not projected on this endpoint. A
		// legacy emitter may still have copied an email into Actor, so keep
		// the activity feed redacted even for those historical rows.
		if strings.Contains(actor, "@") {
			actor = ""
		}
		out = append(out, api.ObsAuditActivityRow{
			ID:    row.ID.String(),
			At:    row.ReceivedAt,
			Kind:  row.Kind,
			Actor: actor,
		})
	}
	return out
}

func projectObsDeployments(rows []state.Deployment) []api.ObsDeploymentRow {
	out := make([]api.ObsDeploymentRow, 0, len(rows))
	for _, row := range rows {
		out = append(out, api.ObsDeploymentRow{
			ID:          row.ID,
			Status:      string(row.Status),
			Kind:        string(row.Kind),
			ImageDigest: row.ImageDigest,
			SourceURL:   row.SourceURL,
			CommitSHA:   row.CommitSHA,
			ErrorCode:   row.ErrorCode,
			CreatedAt:   row.CreatedAt,
		})
	}
	return out
}

func projectObsInstances(rows []state.Instance, slugs, accounts, nodeNames map[string]string) []api.ObsInstanceRow {
	out := make([]api.ObsInstanceRow, 0, len(rows))
	for _, row := range rows {
		item := api.ObsInstanceRow{
			ID:            row.ID,
			AppID:         row.AppID,
			AppSlug:       slugs[row.AppID],
			AccountID:     accounts[row.AppID],
			DeploymentID:  row.DeploymentID,
			NodeID:        row.NodeID,
			NodeName:      nodeNames[row.NodeID],
			State:         row.State,
			RAMMB:         row.RAMMB,
			StartedAt:     row.StartedAt,
			LastRequestAt: row.LastRequestAt,
		}
		if !row.ParkedAt.IsZero() {
			parked := row.ParkedAt
			item.ParkedAt = &parked
		}
		out = append(out, item)
	}
	return out
}

func projectObsNodeApps(apps []state.App, instances []state.Instance) []api.ObsNodeApp {
	type stats struct {
		live, running, waking, coldBooting int
		ram                                int64
		last                               time.Time
	}
	byApp := make(map[string]*stats, len(apps))
	for _, app := range apps {
		byApp[app.ID] = &stats{}
	}
	for _, instance := range instances {
		stat := byApp[instance.AppID]
		if stat == nil {
			continue
		}
		instanceState := state.State(strings.ToLower(instance.State))
		if instanceState.CountsForRAM() {
			stat.live++
			stat.ram += int64(instance.RAMMB) + 8
			if instance.LastRequestAt.After(stat.last) {
				stat.last = instance.LastRequestAt
			}
			switch instanceState {
			case state.StateRunning:
				stat.running++
			case state.StateWaking:
				stat.waking++
			case state.StateColdBooting:
				stat.coldBooting++
			}
		}
	}
	out := make([]api.ObsNodeApp, 0, len(apps))
	for _, app := range apps {
		stat := byApp[app.ID]
		item := api.ObsNodeApp{
			ID:                   app.ID,
			Slug:                 app.Slug,
			AccountID:            app.AccountID,
			Status:               string(app.Status),
			InstancesLive:        stat.live,
			InstancesRunning:     stat.running,
			InstancesWaking:      stat.waking,
			InstancesColdBooting: stat.coldBooting,
			RAMUsedMB:            stat.ram,
		}
		if !stat.last.IsZero() {
			last := stat.last
			item.LastRequestAt = &last
		}
		out = append(out, item)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Slug < out[j].Slug })
	return out
}

func countObsLiveInstances(rows []state.Instance) int {
	// The operator workload surface excludes parked rows but includes
	// every RAM-resident state, including snapshotting and migrating.
	count := 0
	for _, row := range rows {
		if state.IsLive(strings.ToLower(row.State)) {
			count++
		}
	}
	return count
}

func projectObsDrainStatus(rows []state.Instance) api.ObsNodeDrainStatus {
	status := api.ObsNodeDrainStatus{
		TotalInstances: len(rows),
		ObservedAt:     time.Now().UTC(),
	}
	for _, row := range rows {
		switch instanceState := state.State(strings.ToLower(row.State)); instanceState {
		case state.StateRunning:
			status.LiveInstances++
			status.RunningInstances++
		case state.StateWaking:
			status.LiveInstances++
			status.WakingInstances++
		case state.StateColdBooting:
			status.LiveInstances++
			status.ColdBooting++
		case state.StateSnapshotting, state.StateMigrating:
			status.LiveInstances++
		}
	}
	status.DrainSafe = status.LiveInstances == 0
	return status
}

func truncateObsText(value string, max int) string {
	if len(value) <= max {
		return value
	}
	return value[:max] + "…"
}
