package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/state"
)

type runtimeConfigEntryResponse struct {
	Key               string                     `json:"key"`
	Label             string                     `json:"label"`
	Description       string                     `json:"description"`
	Category          string                     `json:"category"`
	Kind              string                     `json:"kind"`
	DefaultValue      json.RawMessage            `json:"default_value"`
	DesiredValue      json.RawMessage            `json:"desired_value"`
	EffectiveValue    json.RawMessage            `json:"effective_value"`
	Scope             string                     `json:"scope"`
	ScopeID           string                     `json:"scope_id,omitempty"`
	RolloutPercent    int                        `json:"rollout_percent"`
	RolloutState      string                     `json:"rollout_state"`
	AutoPromote       bool                       `json:"auto_promote"`
	Source            string                     `json:"source"`
	ApplyMode         string                     `json:"apply_mode"`
	ControllerEnabled bool                       `json:"controller_enabled"`
	Mutable           bool                       `json:"mutable"`
	Sensitive         bool                       `json:"sensitive"`
	Status            string                     `json:"status"`
	LastError         string                     `json:"last_error,omitempty"`
	Version           int64                      `json:"version"`
	UpdatedAt         string                     `json:"updated_at,omitempty"`
	AppliedAt         string                     `json:"applied_at,omitempty"`
	Acks              []runtimeConfigAckResponse `json:"acks,omitempty"`
}

type runtimeConfigAckResponse struct {
	Consumer       string          `json:"consumer"`
	NodeID         string          `json:"node_id,omitempty"`
	Version        int64           `json:"version"`
	Status         string          `json:"status"`
	EffectiveValue json.RawMessage `json:"effective_value,omitempty"`
	Error          string          `json:"error,omitempty"`
	UpdatedAt      string          `json:"updated_at"`
	AppliedAt      string          `json:"applied_at,omitempty"`
}

type runtimeConfigListResponse struct {
	Items       []runtimeConfigEntryResponse `json:"items"`
	GeneratedAt string                       `json:"generated_at"`
}

type runtimeConfigPatchRequest struct {
	Value           json.RawMessage `json:"value"`
	Reason          string          `json:"reason"`
	ExpectedVersion *int64          `json:"expected_version"`
	Scope           string          `json:"scope"`
	ScopeID         string          `json:"scope_id"`
	RolloutPercent  *int            `json:"rollout_percent"`
	AutoPromote     bool            `json:"auto_promote"`
}

type runtimeConfigRollbackRequest struct {
	Version         int64  `json:"version"`
	Reason          string `json:"reason"`
	ExpectedVersion *int64 `json:"expected_version"`
	Scope           string `json:"scope"`
	ScopeID         string `json:"scope_id"`
}

func parseRuntimeConfigTarget(scopeText, scopeID string) (state.RuntimeConfigScope, string, error) {
	scopeText = strings.TrimSpace(scopeText)
	scopeID = strings.TrimSpace(scopeID)
	if scopeText == "" {
		scopeText = string(state.RuntimeConfigScopeGlobal)
	}
	scope := state.RuntimeConfigScope(scopeText)
	switch scope {
	case state.RuntimeConfigScopeGlobal:
		if scopeID != "" {
			return "", "", fmt.Errorf("scope_id must be empty for global configuration")
		}
	case state.RuntimeConfigScopeControlPlane, state.RuntimeConfigScopeDaemon, state.RuntimeConfigScopeNode:
		if scopeID == "" {
			return "", "", fmt.Errorf("scope_id is required for %s configuration", scope)
		}
		if len(scopeID) > 128 {
			return "", "", fmt.Errorf("scope_id must be at most 128 characters")
		}
		if scope == state.RuntimeConfigScopeControlPlane && scopeID != "apid" {
			return "", "", fmt.Errorf("control_plane scope_id must be apid")
		}
	default:
		return "", "", fmt.Errorf("scope must be one of global, control_plane, daemon, or node")
	}
	return scope, scopeID, nil
}

func rolloutPercent(value *int) (int, error) {
	if value == nil {
		return 100, nil
	}
	if *value < 0 || *value > 100 {
		return 0, fmt.Errorf("rollout_percent must be between 0 and 100")
	}
	return *value, nil
}

func runtimeConfigOperationResponse(operation state.RuntimeConfigOperation) api.OperatorRuntimeConfigOperation {
	return api.OperatorRuntimeConfigOperation{
		ID:             operation.ID,
		Key:            operation.Key,
		Scope:          string(operation.Scope),
		ScopeID:        operation.ScopeID,
		Version:        operation.Version,
		DesiredValue:   append(json.RawMessage(nil), operation.DesiredValue...),
		EffectiveValue: append(json.RawMessage(nil), operation.EffectiveValue...),
		ApplyMode:      string(operation.ApplyMode),
		Status:         string(operation.Status),
		Phase:          operation.Phase,
		Error:          operation.Error,
		Reason:         operation.Reason,
		TargetCount:    operation.TargetCount,
		AppliedCount:   operation.AppliedCount,
		FailedCount:    operation.FailedCount,
		RequestedAt:    operation.RequestedAt.UTC().Format(time.RFC3339),
		StartedAt:      timePtrString(operation.StartedAt),
		FinishedAt:     timePtrString(operation.FinishedAt),
	}
}

func timePtrString(value *time.Time) string {
	if value == nil {
		return ""
	}
	return value.UTC().Format(time.RFC3339)
}

// adminRuntimeConfigList handles GET /v1/admin/config. It returns the
// catalog even when a key has never been overridden, which makes the source
// of a value explicit and lets the frontend expose safe defaults without
// reading environment variables or filesystem paths.
func (s *server) adminRuntimeConfigList(w http.ResponseWriter, r *http.Request, acct state.Account) {
	if allowed, prob := s.adminAllows(acct); !allowed {
		api.WriteProblem(w, prob)
		return
	}
	scope, scopeID, err := parseRuntimeConfigTarget(r.URL.Query().Get("scope"), r.URL.Query().Get("scope_id"))
	if err != nil {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation, "Invalid configuration target", err.Error()))
		return
	}
	rows, err := s.store.ListRuntimeConfigs(r.Context(), scope, scopeID)
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not list runtime configuration"))
		return
	}
	byKey := make(map[string]state.RuntimeConfig, len(rows))
	for _, row := range rows {
		byKey[row.Key] = row
	}
	acksByKey := make(map[string][]runtimeConfigAckResponse)
	if ackStore, ok := s.store.(interface {
		ListRuntimeConfigAcks(context.Context, string, state.RuntimeConfigScope, string) ([]state.RuntimeConfigAck, error)
	}); ok {
		acks, ackErr := ackStore.ListRuntimeConfigAcks(r.Context(), "", scope, scopeID)
		if ackErr != nil {
			if s.log != nil {
				s.log.Warn("could not list runtime configuration acknowledgements", "err", ackErr)
			}
		} else {
			for _, ack := range acks {
				acksByKey[ack.Key] = append(acksByKey[ack.Key], runtimeConfigAckResponse{
					Consumer:       ack.Consumer,
					NodeID:         ack.NodeID,
					Version:        ack.Version,
					Status:         string(ack.Status),
					EffectiveValue: append(json.RawMessage(nil), ack.EffectiveValue...),
					Error:          ack.Error,
					UpdatedAt:      ack.UpdatedAt.UTC().Format(time.RFC3339),
					AppliedAt:      timePtrString(ack.AppliedAt),
				})
			}
		}
	}
	items := make([]runtimeConfigEntryResponse, 0, len(runtimeConfigCatalog))
	for _, def := range s.runtimeConfig.Definitions() {
		row, exists := byKey[def.Key]
		item := runtimeConfigEntryResponse{
			Key:               def.Key,
			Label:             def.Label,
			Description:       def.Description,
			Category:          def.Category,
			Kind:              def.Kind,
			DefaultValue:      append(json.RawMessage(nil), def.Default...),
			DesiredValue:      s.runtimeConfig.Value(def.Key),
			EffectiveValue:    s.runtimeConfig.Value(def.Key),
			Scope:             string(scope),
			ScopeID:           scopeID,
			RolloutPercent:    100,
			RolloutState:      string(state.RuntimeConfigRolloutStable),
			AutoPromote:       false,
			Source:            "default_or_environment",
			ApplyMode:         string(def.ApplyMode),
			ControllerEnabled: def.ControllerEnabled,
			Mutable:           def.Mutable,
			Sensitive:         def.Sensitive,
			Status:            string(state.RuntimeConfigApplied),
		}
		if exists {
			item.DesiredValue = append(json.RawMessage(nil), row.DesiredValue...)
			if row.Status == state.RuntimeConfigApplied && len(row.EffectiveValue) > 0 && string(row.EffectiveValue) != "null" {
				item.EffectiveValue = append(json.RawMessage(nil), row.EffectiveValue...)
			}
			item.Source = "operator"
			item.Status = string(row.Status)
			item.LastError = row.LastError
			item.Version = row.Version
			item.RolloutPercent = row.RolloutPercent
			item.RolloutState = string(row.RolloutState)
			item.AutoPromote = row.AutoPromote
			item.UpdatedAt = row.UpdatedAt.UTC().Format("2006-01-02T15:04:05Z07:00")
			if row.AppliedAt != nil {
				item.AppliedAt = row.AppliedAt.UTC().Format("2006-01-02T15:04:05Z07:00")
			}
		}
		item.Acks = append([]runtimeConfigAckResponse(nil), acksByKey[def.Key]...)
		if def.Sensitive {
			item.DesiredValue = json.RawMessage(`"[redacted]"`)
			item.EffectiveValue = json.RawMessage(`"[redacted]"`)
			item.DefaultValue = json.RawMessage(`"[redacted]"`)
			for i := range item.Acks {
				item.Acks[i].EffectiveValue = json.RawMessage(`"[redacted]"`)
			}
		}
		items = append(items, item)
	}
	writeJSON(w, http.StatusOK, runtimeConfigListResponse{
		Items:       items,
		GeneratedAt: nowUTCString(),
	})
}

// adminRuntimeConfigPatch handles PATCH /v1/admin/config/{key}. Hot values
// are applied synchronously. Graceful/rolling/break-glass values become a
// durable operation and return 202; the operator can poll the operation and
// never sees a successful response before a controller has acknowledged the
// effective value.
func (s *server) adminRuntimeConfigPatch(w http.ResponseWriter, r *http.Request, acct state.Account) {
	if allowed, prob := s.adminAllows(acct); !allowed {
		api.WriteProblem(w, prob)
		return
	}
	key := strings.TrimSpace(r.PathValue("key"))
	def, ok := s.runtimeConfig.Definition(key)
	if !ok {
		api.WriteProblem(w, api.NewProblem(http.StatusNotFound, api.CodeNotFound, "Unknown configuration key", "the key is not in the operator configuration catalog"))
		return
	}
	if !def.Mutable {
		api.WriteProblem(w, api.NewProblem(http.StatusConflict, api.CodeConflict, "Configuration is deployment-managed", "this setting requires a rolling or break-glass deployment workflow"))
		return
	}
	var req runtimeConfigPatchRequest
	if err := decodeJSON(r, &req); err != nil || len(req.Value) == 0 {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation, "Invalid configuration value", "value must be valid JSON matching the catalog type"))
		return
	}
	scope, scopeID, err := parseRuntimeConfigTarget(req.Scope, req.ScopeID)
	if err != nil {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation, "Invalid configuration target", err.Error()))
		return
	}
	percent, err := rolloutPercent(req.RolloutPercent)
	if err != nil {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation, "Invalid rollout percentage", err.Error()))
		return
	}
	if percent < 100 && scope != state.RuntimeConfigScopeDaemon {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation, "Invalid rollout target", "percentage rollout is supported only for daemon-scoped settings; use a node scope for an exact target"))
		return
	}
	if req.AutoPromote && (scope != state.RuntimeConfigScopeDaemon || percent >= 100) {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation, "Invalid automatic promotion target", "auto_promote requires a daemon-scoped canary with rollout_percent below 100"))
		return
	}
	if (scope == state.RuntimeConfigScopeDaemon || scope == state.RuntimeConfigScopeNode) && def.ApplyMode != state.RuntimeConfigApplyHot {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation, "Invalid scoped setting", "daemon and node targets require a hot setting with a live runtime watcher"))
		return
	}
	if err := validateRuntimeConfigValue(def, req.Value); err != nil {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation, "Invalid configuration value", err.Error()))
		return
	}
	if current, promoting, promotionErr := runtimeConfigPromotionRequested(s.store, key, scope, scopeID, percent, r.Context()); promotionErr != nil {
		api.WriteProblem(w, api.ErrCapacity("could not inspect runtime configuration rollout"))
		return
	} else if promoting {
		if healthErr, unavailable := s.runtimeConfigPromotionHealth(r.Context(), current); healthErr != nil {
			api.WriteProblem(w, runtimeConfigPromotionProblem(healthErr, unavailable))
			return
		}
	}
	if len(req.Reason) < 3 || len(req.Reason) > 500 {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation, "Invalid change reason", "reason must be 3..500 characters"))
		return
	}
	row, err := s.store.UpsertRuntimeConfig(r.Context(), state.RuntimeConfigUpdate{
		Key:             key,
		Scope:           scope,
		ScopeID:         scopeID,
		DesiredValue:    req.Value,
		RolloutPercent:  &percent,
		AutoPromote:     req.AutoPromote,
		ApplyMode:       def.ApplyMode,
		ActorID:         acct.ID,
		Reason:          req.Reason,
		ExpectedVersion: req.ExpectedVersion,
	})
	if err != nil {
		if errors.Is(err, state.ErrRuntimeConfigConflict) {
			api.WriteProblem(w, api.NewProblem(http.StatusConflict, api.CodeConflict, "Configuration changed concurrently", "refresh the configuration page and retry with the latest version"))
			return
		}
		api.WriteProblem(w, api.ErrCapacity("could not save runtime configuration"))
		return
	}
	if def.ApplyMode != state.RuntimeConfigApplyHot {
		operation, err := s.store.CreateRuntimeConfigOperation(r.Context(), row, acct.ID, req.Reason)
		if err != nil {
			api.WriteProblem(w, api.ErrCapacity("could not create runtime configuration operation"))
			return
		}
		if s.audit != nil {
			s.audit.Emit(r.Context(), "operator.runtime_config_apply_requested", nil, map[string]any{
				"key": key, "version": row.Version, "apply_mode": def.ApplyMode,
				"operation_id": operation.ID, "reason": req.Reason, "actor": acct.ID,
				"scope": scope, "scope_id": scopeID, "rollout_percent": percent, "auto_promote": req.AutoPromote,
			})
		}
		if !def.ControllerEnabled {
			// A durable operation without a production consumer would remain
			// pending forever and mislead the operator into waiting for an
			// apply that cannot happen. Fail closed with a terminal, auditable
			// state until the corresponding daemon controller is deployed.
			reason := fmt.Sprintf("no production controller is enabled for %s apply; use a rolling deployment", def.ApplyMode)
			if blockErr := s.store.MarkRuntimeConfigOperationBlocked(r.Context(), operation.ID, "controller_unavailable", reason); blockErr != nil {
				api.WriteProblem(w, api.ErrCapacity("could not finalize runtime configuration operation"))
				return
			}
			if s.audit != nil {
				s.audit.Emit(r.Context(), "operator.runtime_config_apply_blocked", nil, map[string]any{
					"key": key, "version": row.Version, "apply_mode": def.ApplyMode,
					"operation_id": operation.ID, "reason": reason, "actor": acct.ID,
					"scope": scope, "scope_id": scopeID, "rollout_percent": percent,
				})
			}
			operation, err = s.store.GetRuntimeConfigOperation(r.Context(), operation.ID)
			if err != nil {
				api.WriteProblem(w, api.ErrCapacity("could not read runtime configuration operation"))
				return
			}
		}
		_ = s.notif.Notify(r.Context(), db.NotifyRuntimeConfigOperationChanged, operation.ID)
		w.Header().Set("Location", "/v1/admin/config-operations/"+operation.ID)
		writeJSON(w, http.StatusAccepted, runtimeConfigOperationResponse(operation))
		return
	}
	var applyErr error
	if scope == state.RuntimeConfigScopeDaemon || scope == state.RuntimeConfigScopeNode {
		// The edge watcher owns the process-local apply for scoped rows. Mark
		// the durable target effective now so matching daemons can consume it;
		// non-matching daemons retain the lower-precedence value.
		applyErr = s.store.MarkRuntimeConfigApplied(r.Context(), row.Key, row.Scope, row.ScopeID, row.Version, row.DesiredValue, "")
	} else {
		applyErr = s.applyHotRuntimeConfig(r.Context(), row)
	}
	if applyErr != nil {
		if errors.Is(applyErr, state.ErrRuntimeConfigConflict) {
			api.WriteProblem(w, api.NewProblem(http.StatusConflict, api.CodeConflict, "Configuration changed concurrently", "refresh the configuration page and retry with the latest version"))
			return
		}
		api.WriteProblem(w, api.ErrCapacity("could not acknowledge runtime configuration"))
		return
	}
	if s.audit != nil {
		s.audit.Emit(r.Context(), "operator.runtime_config_changed", nil, map[string]any{
			"key": key, "version": row.Version, "value": req.Value,
			"apply_mode": def.ApplyMode, "reason": req.Reason, "actor": acct.ID,
			"scope": scope, "scope_id": scopeID, "rollout_percent": percent, "auto_promote": req.AutoPromote,
		})
	}
	// The trigger is the production notification path. The explicit call is
	// retained for MemStore/no-trigger test fixtures and is harmless in
	// Postgres because subscribers reconcile idempotently by version.
	_ = s.notif.Notify(r.Context(), db.NotifyRuntimeConfigChanged, key)
	row.EffectiveValue = append(json.RawMessage(nil), req.Value...)
	row.Status = state.RuntimeConfigApplied
	writeJSON(w, http.StatusOK, runtimeConfigEntryResponse{
		Key:               def.Key,
		Label:             def.Label,
		Description:       def.Description,
		Category:          def.Category,
		Kind:              def.Kind,
		DefaultValue:      append(json.RawMessage(nil), def.Default...),
		DesiredValue:      append(json.RawMessage(nil), req.Value...),
		EffectiveValue:    append(json.RawMessage(nil), req.Value...),
		Scope:             string(scope),
		ScopeID:           scopeID,
		RolloutPercent:    percent,
		RolloutState:      string(row.RolloutState),
		AutoPromote:       row.AutoPromote,
		Source:            "operator",
		ApplyMode:         string(def.ApplyMode),
		ControllerEnabled: def.ControllerEnabled,
		Mutable:           def.Mutable,
		Sensitive:         def.Sensitive,
		Status:            string(state.RuntimeConfigApplied),
		Version:           row.Version,
		UpdatedAt:         row.UpdatedAt.UTC().Format(time.RFC3339),
		AppliedAt:         nowUTCString(),
	})
}

// applyHotRuntimeConfig is the single in-process apply path for synchronous
// settings and rollbacks. It intentionally acknowledges the durable row only
// after the versioned snapshot has been swapped. If the acknowledgement loses
// a race, the newer database version is reconciled immediately; a transient
// database error leaves the new value live and the subscriber repairs the
// acknowledgement later, so a database hiccup never requires an apid restart.
func (s *server) applyHotRuntimeConfig(ctx context.Context, row state.RuntimeConfig) error {
	applied, err := s.runtimeConfig.applyScopedVersion(row)
	if err != nil {
		_ = s.store.MarkRuntimeConfigApplied(ctx, row.Key, row.Scope, row.ScopeID, row.Version, nil, err.Error())
		return err
	}
	if !applied {
		_ = s.runtimeConfig.reconcile(ctx, s.store)
		return state.ErrRuntimeConfigConflict
	}
	if err := s.store.MarkRuntimeConfigApplied(ctx, row.Key, row.Scope, row.ScopeID, row.Version, row.DesiredValue, ""); err != nil {
		if errors.Is(err, state.ErrRuntimeConfigConflict) {
			_ = s.runtimeConfig.reconcile(ctx, s.store)
		}
		return err
	}
	return nil
}

// adminRuntimeConfigRollback applies a previous hot revision as a new
// version. The original revision remains immutable in the history, so the
// rollback itself is auditable and can be rolled forward again if needed.
func (s *server) adminRuntimeConfigRollback(w http.ResponseWriter, r *http.Request, acct state.Account) {
	if allowed, prob := s.adminAllows(acct); !allowed {
		api.WriteProblem(w, prob)
		return
	}
	key := strings.TrimSpace(r.PathValue("key"))
	def, ok := s.runtimeConfig.Definition(key)
	if !ok {
		api.WriteProblem(w, api.NewProblem(http.StatusNotFound, api.CodeNotFound, "Unknown configuration key", "the key is not in the operator configuration catalog"))
		return
	}
	if !def.Mutable || def.ApplyMode != state.RuntimeConfigApplyHot {
		api.WriteProblem(w, api.NewProblem(http.StatusConflict, api.CodeConflict, "Only hot settings can be rolled back", "this setting requires its deployment-managed apply workflow"))
		return
	}
	var req runtimeConfigRollbackRequest
	if err := decodeJSON(r, &req); err != nil || req.Version < 1 {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation, "Invalid rollback revision", "version must be a positive revision number"))
		return
	}
	if len(req.Reason) < 3 || len(req.Reason) > 500 {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation, "Invalid change reason", "reason must be 3..500 characters"))
		return
	}
	scope, scopeID, err := parseRuntimeConfigTarget(req.Scope, req.ScopeID)
	if err != nil {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation, "Invalid configuration target", err.Error()))
		return
	}
	if (scope == state.RuntimeConfigScopeDaemon || scope == state.RuntimeConfigScopeNode) && def.ApplyMode != state.RuntimeConfigApplyHot {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation, "Invalid scoped setting", "daemon and node targets require a hot setting with a live runtime watcher"))
		return
	}
	current, err := s.store.GetRuntimeConfig(r.Context(), key, scope, scopeID)
	if err != nil {
		if errors.Is(err, state.ErrRuntimeConfigNotFound) {
			api.WriteProblem(w, api.NewProblem(http.StatusNotFound, api.CodeNotFound, "Configuration has no revisions", "apply a setting once before rolling it back"))
			return
		}
		api.WriteProblem(w, api.ErrCapacity("could not read runtime configuration"))
		return
	}
	if req.Version >= current.Version {
		api.WriteProblem(w, api.NewProblem(http.StatusConflict, api.CodeConflict, "Configuration revision is already current", "choose an older revision to create a rollback"))
		return
	}
	if req.ExpectedVersion != nil && *req.ExpectedVersion != current.Version {
		api.WriteProblem(w, api.NewProblem(http.StatusConflict, api.CodeConflict, "Configuration changed concurrently", "refresh the configuration page and retry with the latest version"))
		return
	}
	revision, err := s.store.GetRuntimeConfigRevision(r.Context(), key, scope, scopeID, req.Version)
	if err != nil {
		if errors.Is(err, state.ErrRuntimeConfigNotFound) {
			api.WriteProblem(w, api.NewProblem(http.StatusNotFound, api.CodeNotFound, "Configuration revision not found", "the requested revision is not available for this setting"))
			return
		}
		api.WriteProblem(w, api.ErrCapacity("could not read configuration revision"))
		return
	}
	if err := validateRuntimeConfigValue(def, revision.NewValue); err != nil {
		api.WriteProblem(w, api.NewProblem(http.StatusConflict, api.CodeConflict, "Configuration revision is incompatible", "the catalog type changed and this revision cannot be applied"))
		return
	}
	reason := fmt.Sprintf("rollback to v%d: %s", req.Version, req.Reason)
	expectedVersion := current.Version
	if req.ExpectedVersion != nil {
		expectedVersion = *req.ExpectedVersion
	}
	row, err := s.store.UpsertRuntimeConfig(r.Context(), state.RuntimeConfigUpdate{
		Key:             key,
		Scope:           scope,
		ScopeID:         scopeID,
		DesiredValue:    revision.NewValue,
		RolloutPercent:  &revision.RolloutPercent,
		AutoPromote:     revision.AutoPromote,
		ApplyMode:       state.RuntimeConfigApplyHot,
		ActorID:         acct.ID,
		Reason:          reason,
		ExpectedVersion: &expectedVersion,
	})
	if err != nil {
		if errors.Is(err, state.ErrRuntimeConfigConflict) {
			api.WriteProblem(w, api.NewProblem(http.StatusConflict, api.CodeConflict, "Configuration changed concurrently", "refresh the configuration page and retry with the latest version"))
			return
		}
		api.WriteProblem(w, api.ErrCapacity("could not save runtime configuration rollback"))
		return
	}
	var applyErr error
	if scope == state.RuntimeConfigScopeDaemon || scope == state.RuntimeConfigScopeNode {
		applyErr = s.store.MarkRuntimeConfigApplied(r.Context(), row.Key, row.Scope, row.ScopeID, row.Version, row.DesiredValue, "")
	} else {
		applyErr = s.applyHotRuntimeConfig(r.Context(), row)
	}
	if applyErr != nil {
		if errors.Is(applyErr, state.ErrRuntimeConfigConflict) {
			api.WriteProblem(w, api.NewProblem(http.StatusConflict, api.CodeConflict, "Configuration changed concurrently", "refresh the configuration page and retry with the latest version"))
			return
		}
		api.WriteProblem(w, api.ErrCapacity("could not apply runtime configuration rollback"))
		return
	}
	if s.audit != nil {
		s.audit.Emit(r.Context(), "operator.runtime_config_rollback", nil, map[string]any{
			"key": key, "version": row.Version, "rollback_target_version": req.Version,
			"value": revision.NewValue, "reason": req.Reason, "actor": acct.ID,
			"scope": scope, "scope_id": scopeID, "rollout_percent": revision.RolloutPercent, "auto_promote": revision.AutoPromote,
		})
	}
	_ = s.notif.Notify(r.Context(), db.NotifyRuntimeConfigChanged, key)
	row.EffectiveValue = append(json.RawMessage(nil), row.DesiredValue...)
	row.Status = state.RuntimeConfigApplied
	writeJSON(w, http.StatusOK, runtimeConfigEntryResponse{
		Key: def.Key, Label: def.Label, Description: def.Description,
		Category: def.Category, Kind: def.Kind,
		DefaultValue:   append(json.RawMessage(nil), def.Default...),
		DesiredValue:   append(json.RawMessage(nil), row.DesiredValue...),
		EffectiveValue: append(json.RawMessage(nil), row.EffectiveValue...),
		Scope:          string(scope), ScopeID: scopeID, RolloutPercent: revision.RolloutPercent,
		RolloutState: string(row.RolloutState),
		AutoPromote:  row.AutoPromote,
		Source:       "operator", ApplyMode: string(def.ApplyMode), Mutable: def.Mutable,
		ControllerEnabled: def.ControllerEnabled,
		Sensitive:         def.Sensitive, Status: string(state.RuntimeConfigApplied),
		Version: row.Version, UpdatedAt: row.UpdatedAt.UTC().Format(time.RFC3339), AppliedAt: nowUTCString(),
	})
}

// adminRuntimeConfigOperationGet handles GET
// /v1/admin/config-operations/{id}. Polling is deliberately MFA-free after
// the MFA-gated write, matching the existing operator-intent polling surface.
func (s *server) adminRuntimeConfigOperationGet(w http.ResponseWriter, r *http.Request, acct state.Account) {
	if allowed, prob := s.adminAllows(acct); !allowed {
		api.WriteProblem(w, prob)
		return
	}
	operation, err := s.store.GetRuntimeConfigOperation(r.Context(), strings.TrimSpace(r.PathValue("id")))
	if err != nil {
		if errors.Is(err, state.ErrRuntimeConfigNotFound) {
			api.WriteProblem(w, api.NewProblem(http.StatusNotFound, api.CodeNotFound, "Configuration operation not found", "no operation exists with that id"))
			return
		}
		api.WriteProblem(w, api.ErrCapacity("could not read configuration operation"))
		return
	}
	writeJSON(w, http.StatusOK, runtimeConfigOperationResponse(operation))
}

func (s *server) adminRuntimeConfigRevisions(w http.ResponseWriter, r *http.Request, acct state.Account) {
	if allowed, prob := s.adminAllows(acct); !allowed {
		api.WriteProblem(w, prob)
		return
	}
	key := strings.TrimSpace(r.PathValue("key"))
	def, ok := s.runtimeConfig.Definition(key)
	if !ok {
		api.WriteProblem(w, api.NewProblem(http.StatusNotFound, api.CodeNotFound, "Unknown configuration key", "the key is not in the operator configuration catalog"))
		return
	}
	scope, scopeID, err := parseRuntimeConfigTarget(r.URL.Query().Get("scope"), r.URL.Query().Get("scope_id"))
	if err != nil {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation, "Invalid configuration target", err.Error()))
		return
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	revisions, err := s.store.ListRuntimeConfigRevisions(r.Context(), key, scope, scopeID, limit)
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not list configuration revisions"))
		return
	}
	items := make([]api.OperatorRuntimeConfigRevision, 0, len(revisions))
	for _, revision := range revisions {
		oldValue := append(json.RawMessage(nil), revision.OldValue...)
		newValue := append(json.RawMessage(nil), revision.NewValue...)
		if def.Sensitive {
			oldValue = json.RawMessage(`"[redacted]"`)
			newValue = json.RawMessage(`"[redacted]"`)
		}
		items = append(items, api.OperatorRuntimeConfigRevision{
			ID: revision.ID, Key: revision.Key, Scope: string(revision.Scope), ScopeID: revision.ScopeID,
			Version: revision.Version, RolloutPercent: revision.RolloutPercent, AutoPromote: revision.AutoPromote, OldValue: oldValue, NewValue: newValue,
			ActorID: revision.ActorID, Reason: revision.Reason,
			CreatedAt: revision.CreatedAt.UTC().Format(time.RFC3339),
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func nowUTCString() string {
	return time.Now().UTC().Format("2006-01-02T15:04:05Z07:00")
}
