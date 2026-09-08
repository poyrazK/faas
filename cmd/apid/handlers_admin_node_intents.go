package main

// Provider compute-node lifecycle operations are durable intents. The HTTP
// request only validates and records desired state; schedd owns dispatch and
// lifecycle CAS, while the recovery runner owns migration/recreation. This
// makes the operations console a safe client of the owning controller rather
// than another direct writer to compute_nodes.

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/middleware"
	"github.com/onebox-faas/faas/pkg/state"
)

type nodeIntentSpec struct {
	kind               state.OperatorIntentKind
	requestedLifecycle state.NodeLifecycle
	requestedActive    bool
}

type nodeIntentMetadata struct {
	NodeName           string                        `json:"node_name"`
	PreviousLifecycle  string                        `json:"previous_lifecycle"`
	RequestedLifecycle string                        `json:"requested_lifecycle"`
	PreviousActive     bool                          `json:"previous_active"`
	RequestedActive    bool                          `json:"requested_active"`
	Forced             bool                          `json:"forced"`
	Preflight          api.ObsNodeOperationPreflight `json:"preflight"`
}

func (s *server) enqueueObsNodeMutation(w http.ResponseWriter, r *http.Request, acct state.Account, action string, forced bool) {
	if allowed, prob := s.adminAllows(acct); !allowed {
		api.WriteProblem(w, prob)
		return
	}
	reason, ok := parseNodeIntentConfirmation(w, r, action)
	if !ok {
		return
	}
	spec, err := nodeIntentSpecFor(action)
	if err != nil {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation, "unsupported node action", err.Error()))
		return
	}
	node, err := s.store.ComputeNodeByName(r.Context(), r.PathValue("name"))
	if err != nil {
		api.WriteProblem(w, api.NewProblem(http.StatusNotFound, api.CodeNotFound, "Node not found", err.Error()))
		return
	}
	if current := effectiveNodeLifecycle(node); !nodeIntentTransitionAllowed(spec.kind, current) {
		api.WriteProblem(w, api.ErrNodeLifecycleInvalid(string(current), string(spec.requestedLifecycle)))
		return
	}
	preflight, err := s.nodeIntentPreflight(r, node, spec, forced)
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not build compute-node preflight"))
		return
	}
	s.enqueueNodeIntent(w, r, acct, node, spec, reason, forced, preflight)
}

func effectiveNodeLifecycle(node state.ComputeNode) state.NodeLifecycle {
	if node.Lifecycle != "" {
		return node.Lifecycle
	}
	if node.Active {
		return state.NodeLifecycleActive
	}
	return state.NodeLifecycleUnavailable
}

func nodeIntentTransitionAllowed(kind state.OperatorIntentKind, current state.NodeLifecycle) bool {
	switch kind {
	case state.OperatorIntentKindNodeDrain:
		return current == state.NodeLifecycleActive || current == state.NodeLifecycleDraining
	case state.OperatorIntentKindNodeActivate:
		return current == state.NodeLifecycleUnavailable || current == state.NodeLifecycleMaintenance || current == state.NodeLifecycleActive
	case state.OperatorIntentKindNodeForceDrain:
		return true
	default:
		return false
	}
}

func parseNodeIntentConfirmation(w http.ResponseWriter, r *http.Request, action string) (string, bool) {
	if r.URL.Query().Get("confirm") != "true" {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation, "confirm required", "?confirm=true is required for compute-node state changes"))
		return "", false
	}
	reason := r.URL.Query().Get("reason")
	if reason == "" {
		reason = "operator_" + strings.ReplaceAll(action, "-", "_")
	}
	if len(reason) > obsOpsReasonMaxLen || !obsOpsReasonShape.MatchString(reason) {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation, "invalid reason", "reason must match [a-z0-9_]{1,64}"))
		return "", false
	}
	return reason, true
}

func nodeIntentSpecFor(action string) (nodeIntentSpec, error) {
	switch action {
	case "drain":
		return nodeIntentSpec{
			kind:               state.OperatorIntentKindNodeDrain,
			requestedLifecycle: state.NodeLifecycleMaintenance,
			requestedActive:    false,
		}, nil
	case "force-drain":
		return nodeIntentSpec{
			kind:               state.OperatorIntentKindNodeForceDrain,
			requestedLifecycle: state.NodeLifecycleMaintenance,
			requestedActive:    false,
		}, nil
	case "activate":
		return nodeIntentSpec{
			kind:               state.OperatorIntentKindNodeActivate,
			requestedLifecycle: state.NodeLifecycleActive,
			requestedActive:    true,
		}, nil
	default:
		return nodeIntentSpec{}, fmt.Errorf("unknown action %q", action)
	}
}

func (s *server) nodeIntentPreflight(r *http.Request, node state.ComputeNode, spec nodeIntentSpec, forced bool) (api.ObsNodeOperationPreflight, error) {
	instances, err := s.store.ListInstancesOnNodeID(r.Context(), node.ID)
	if err != nil {
		return api.ObsNodeOperationPreflight{}, err
	}
	appIDs := make(map[string]struct{})
	tenantIDs := make(map[string]struct{})
	var liveRAMMB int64
	for _, instance := range instances {
		appIDs[instance.AppID] = struct{}{}
		if state.IsLive(strings.ToLower(instance.State)) {
			liveRAMMB += int64(instance.RAMMB + api.PerVMOverheadMB)
		}
	}
	for appID := range appIDs {
		app, appErr := s.store.AppByID(r.Context(), appID)
		if appErr != nil {
			return api.ObsNodeOperationPreflight{}, fmt.Errorf("read app %s for node preflight: %w", appID, appErr)
		}
		if app.AccountID != "" {
			tenantIDs[app.AccountID] = struct{}{}
		}
	}
	capacityChange := 0
	if spec.requestedActive && !node.Active {
		capacityChange = node.AdmissionCeilingMB
	} else if !spec.requestedActive && node.Active {
		capacityChange = -node.AdmissionCeilingMB
	}
	live := countObsLiveInstances(instances)
	return api.ObsNodeOperationPreflight{
		AffectedApps:      len(appIDs),
		AffectedTenants:   len(tenantIDs),
		TotalInstances:    len(instances),
		LiveInstances:     live,
		LiveRAMMB:         liveRAMMB,
		CapacityChangeMB:  capacityChange,
		Reversible:        !forced,
		DisruptionWarning: forced && live > 0,
	}, nil
}

func (s *server) enqueueNodeIntent(w http.ResponseWriter, r *http.Request, acct state.Account, node state.ComputeNode, spec nodeIntentSpec, reason string, forced bool, preflight api.ObsNodeOperationPreflight) {
	previousLifecycle := effectiveNodeLifecycle(node)
	metadata := nodeIntentMetadata{
		NodeName:           node.Name,
		PreviousLifecycle:  string(previousLifecycle),
		RequestedLifecycle: string(spec.requestedLifecycle),
		PreviousActive:     node.Active,
		RequestedActive:    spec.requestedActive,
		Forced:             forced,
		Preflight:          preflight,
	}
	rawMetadata, err := json.Marshal(metadata)
	if err != nil {
		api.WriteProblem(w, api.NewProblem(http.StatusInternalServerError, api.CodeInternal, "encode operator intent failed", err.Error()))
		return
	}
	intentID, err := s.store.InsertOperatorIntent(r.Context(), spec.kind, node.ID, nil, acct.ID, reason, rawMetadata, middleware.TraceIDFrom(r))
	if err != nil {
		api.WriteProblem(w, api.NewProblem(http.StatusInternalServerError, api.CodeInternal, "insert operator intent failed", err.Error()))
		return
	}
	s.notifyNodeIntent(r, intentID, spec.kind, node.ID)
	s.emitNodeIntentEnqueued(r, acct, node, spec, intentID, reason, forced, preflight, previousLifecycle)
	writeJSON(w, http.StatusAccepted, api.ObsNodeMutationResponse{
		OK:                 true,
		IntentID:           intentID,
		StatusURL:          "/v1/admin/operator-intents/" + intentID,
		ExpiresAt:          time.Now().UTC().Add(operatorIntentPollHorizon),
		Kind:               string(spec.kind),
		Node:               node.Name,
		PreviousActive:     node.Active,
		Active:             spec.requestedActive,
		PreviousLifecycle:  string(previousLifecycle),
		RequestedLifecycle: string(spec.requestedLifecycle),
		LiveInstances:      preflight.LiveInstances,
		Forced:             forced,
		Reason:             reason,
		Preflight:          preflight,
	})
}

func (s *server) notifyNodeIntent(r *http.Request, intentID string, kind state.OperatorIntentKind, nodeID string) {
	payload, _ := json.Marshal(map[string]string{
		"intent_id": intentID,
		"kind":      string(kind),
		"target_id": nodeID,
	})
	if err := s.notif.Notify(r.Context(), db.NotifyOperatorIntent, string(payload)); err != nil {
		s.log.Warn("apid: node operator_intent notify failed", "intent_id", intentID, "err", err)
	}
}

func (s *server) emitNodeIntentEnqueued(r *http.Request, acct state.Account, node state.ComputeNode, spec nodeIntentSpec, intentID, reason string, forced bool, preflight api.ObsNodeOperationPreflight, previous state.NodeLifecycle) {
	if s.audit == nil {
		return
	}
	subject := node.ID
	s.audit.Emit(r.Context(), "operator.action."+string(spec.kind), &subject, map[string]any{
		"actor":               acct.ID,
		"intent_id":           intentID,
		"node_id":             node.ID,
		"node_name":           node.Name,
		"previous_lifecycle":  previous,
		"requested_lifecycle": spec.requestedLifecycle,
		"forced":              forced,
		"reason":              reason,
		"preflight":           preflight,
		"result":              "enqueued",
	})
}
