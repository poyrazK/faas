package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/realtime"
	"github.com/onebox-faas/faas/pkg/state"
)

// realtimeOwner is the routing seam for live connection operations. The
// endpoint ID is deliberately present on every operation, even where the
// local daemon only needs a connection ID: a leased cross-node implementation
// uses it to resolve the owning realtime node and enforce endpoint scope.
type realtimeOwner interface {
	Send(context.Context, string, string, realtime.Message) error
	CloseConnection(context.Context, string, string, string) error
	Subscribe(context.Context, string, string, string) error
	Unsubscribe(context.Context, string, string, string) error
	Publish(context.Context, string, string, realtime.Message) (int, error)
}

type realtimeConnectionInventory interface {
	ListConnectionInventory(context.Context) (realtime.ConnectionInventory, error)
}

const (
	managedRealtimeConnectionsLimitDefault = 100
	managedRealtimeConnectionsLimitMax     = 1000
	managedRealtimeDrainConnectionIDsMax   = 100
)

// localRealtimeOwner adapts the Unix management client to realtimeOwner. The
// local adapter verifies the connection's endpoint ID before issuing the
// operation; the public handler separately checks that the endpoint belongs to
// the authenticated account.
type localRealtimeOwner struct {
	client *realtime.Client
}

func (o localRealtimeOwner) ownsConnection(ctx context.Context, endpointID, connectionID string) error {
	connections, err := o.client.Connections(ctx)
	if err != nil {
		return err
	}
	for _, connection := range connections {
		if connection.ID == connectionID && connection.EndpointID == endpointID {
			return nil
		}
	}
	return realtime.ErrConnectionNotFound
}

func (o localRealtimeOwner) Send(ctx context.Context, endpointID, connectionID string, message realtime.Message) error {
	if err := o.ownsConnection(ctx, endpointID, connectionID); err != nil {
		return err
	}
	return o.client.Send(ctx, connectionID, message)
}

func (o localRealtimeOwner) CloseConnection(ctx context.Context, endpointID, connectionID, reason string) error {
	if err := o.ownsConnection(ctx, endpointID, connectionID); err != nil {
		return err
	}
	return o.client.CloseConnection(ctx, connectionID, reason)
}

func (o localRealtimeOwner) Subscribe(ctx context.Context, endpointID, connectionID, channel string) error {
	if err := o.ownsConnection(ctx, endpointID, connectionID); err != nil {
		return err
	}
	return o.client.Subscribe(ctx, connectionID, channel)
}

func (o localRealtimeOwner) Unsubscribe(ctx context.Context, endpointID, connectionID, channel string) error {
	if err := o.ownsConnection(ctx, endpointID, connectionID); err != nil {
		return err
	}
	return o.client.Unsubscribe(ctx, connectionID, channel)
}

func (o localRealtimeOwner) Publish(ctx context.Context, endpointID, channel string, message realtime.Message) (int, error) {
	return o.client.Publish(ctx, endpointID, channel, message)
}

func (s *server) managedRealtimeOwner(w http.ResponseWriter) (realtimeOwner, bool) {
	if s.realtimeOwner == nil {
		api.WriteProblem(w, api.ErrCapacity("managed realtime owner unavailable"))
		return nil, false
	}
	return s.realtimeOwner, true
}

func decodeManagedRealtimeMessage(r *http.Request) (realtime.Message, *api.Problem) {
	var request api.ManagedRealtimeMessageRequest
	// Base64 expands a payload by roughly 4/3; allow that overhead while
	// keeping the decoded message bounded by the daemon's 1 MiB default.
	if err := decodeJSONSized(r, &request, 2<<20); err != nil {
		return realtime.Message{}, api.ErrRealtimeInvalid(err.Error())
	}
	data, err := base64.StdEncoding.DecodeString(request.DataBase64)
	if err != nil {
		return realtime.Message{}, api.ErrRealtimeInvalid("data_base64 must be valid standard base64")
	}
	if len(data) > api.RealtimeMessageMaxBytes {
		return realtime.Message{}, api.ErrRealtimeInvalid(fmt.Sprintf("message exceeds %d bytes", api.RealtimeMessageMaxBytes))
	}
	return realtime.Message{Data: data, Binary: request.Binary}, nil
}

func validateManagedRealtimeChannel(channel string) *api.Problem {
	if !realtime.ValidateChannel(channel) {
		return api.ErrRealtimeInvalid("channel must be non-empty, at most 256 bytes, and contain no '/', '?', '#', or whitespace padding")
	}
	return nil
}

func (s *server) managedRealtimeEndpointAction(w http.ResponseWriter, r *http.Request, acct state.Account) (state.ManagedRealtimeEndpoint, realtimeOwner, bool) {
	row, _, ok := s.loadManagedRealtimeEndpoint(w, r, acct)
	if !ok {
		return state.ManagedRealtimeEndpoint{}, nil, false
	}
	owner, ok := s.managedRealtimeOwner(w)
	if !ok {
		return state.ManagedRealtimeEndpoint{}, nil, false
	}
	return row, owner, true
}

func (s *server) listManagedRealtimeConnections(w http.ResponseWriter, r *http.Request, acct state.Account) {
	row, _, ok := s.loadManagedRealtimeEndpoint(w, r, acct)
	if !ok {
		return
	}
	lister, ok := s.realtimeOwner.(realtimeConnectionInventory)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("managed realtime owner unavailable"))
		return
	}
	prob, limit := api.ParseLimit(r.URL.Query().Get("limit"), managedRealtimeConnectionsLimitDefault, managedRealtimeConnectionsLimitMax, "connections")
	if prob != nil {
		api.WriteProblem(w, prob)
		return
	}
	channel := r.URL.Query().Get("channel")
	if channel != "" {
		if problem := validateManagedRealtimeChannel(channel); problem != nil {
			api.WriteProblem(w, problem)
			return
		}
	}
	inventory, err := lister.ListConnectionInventory(r.Context())
	if err != nil && inventory.NodesQueried == 0 {
		api.WriteProblem(w, api.ErrCapacity("managed realtime owner unavailable"))
		return
	}
	sort.Slice(inventory.Connections, func(i, j int) bool {
		return inventory.Connections[i].ID < inventory.Connections[j].ID
	})
	connections := make([]api.ManagedRealtimeConnectionResponse, 0, min(limit, len(inventory.Connections)))
	truncated := false
	for _, connection := range inventory.Connections {
		if connection.EndpointID != row.ID || connection.AppID != row.AppID || connection.AccountID != acct.ID {
			continue
		}
		if channel != "" && !realtimeConnectionHasChannel(connection, channel) {
			continue
		}
		if len(connections) == limit {
			truncated = true
			break
		}
		connections = append(connections, api.ManagedRealtimeConnectionResponse{
			ID:          connection.ID,
			EndpointID:  connection.EndpointID,
			AppID:       connection.AppID,
			AccountID:   connection.AccountID,
			Principal:   connection.Principal,
			ConnectedAt: api.FormatAlertTime(connection.Connected),
			LastSeenAt:  api.FormatAlertTime(connection.LastSeen),
			ExpiresAt:   api.FormatAlertTime(connection.Expires),
			Channels:    append([]string(nil), connection.Channels...),
		})
	}
	writeJSON(w, http.StatusOK, api.ManagedRealtimeConnectionListResponse{
		Connections:      connections,
		Limit:            limit,
		Truncated:        truncated,
		Partial:          inventory.NodesUnavailable > 0,
		NodesQueried:     inventory.NodesQueried,
		NodesUnavailable: inventory.NodesUnavailable,
	})
}

func realtimeConnectionHasChannel(connection realtime.ConnectionInfo, channel string) bool {
	for _, value := range connection.Channels {
		if value == channel {
			return true
		}
	}
	return false
}

const (
	managedRealtimeDrainStatusWouldClose = "would_close"
	managedRealtimeDrainStatusClosed     = "closed"
	managedRealtimeDrainStatusGone       = "gone"
	managedRealtimeDrainStatusFailed     = "failed"
)

func (s *server) drainManagedRealtimeConnections(w http.ResponseWriter, r *http.Request, acct state.Account) {
	row, owner, ok := s.managedRealtimeEndpointAction(w, r, acct)
	if !ok {
		return
	}
	lister, ok := owner.(realtimeConnectionInventory)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("managed realtime owner unavailable"))
		return
	}
	var request api.ManagedRealtimeDrainRequest
	if err := decodeJSONSized(r, &request, 64<<10); err != nil {
		api.WriteProblem(w, api.ErrRealtimeInvalid(err.Error()))
		return
	}
	request.Reason = strings.TrimSpace(request.Reason)
	if request.Reason == "" {
		api.WriteProblem(w, api.ErrRealtimeInvalid("reason is required"))
		return
	}
	if len(request.Reason) > 256 {
		api.WriteProblem(w, api.ErrRealtimeInvalid("reason exceeds 256 bytes"))
		return
	}
	request.Principal = strings.TrimSpace(request.Principal)
	if len(request.Principal) > 256 {
		api.WriteProblem(w, api.ErrRealtimeInvalid("principal exceeds 256 bytes"))
		return
	}
	if request.Channel != "" {
		if problem := validateManagedRealtimeChannel(request.Channel); problem != nil {
			api.WriteProblem(w, problem)
			return
		}
	}
	if request.Limit == 0 {
		request.Limit = managedRealtimeConnectionsLimitDefault
	}
	if request.Limit < 1 || request.Limit > managedRealtimeConnectionsLimitMax {
		api.WriteProblem(w, api.ErrRealtimeInvalid(fmt.Sprintf("limit must be between 1 and %d", managedRealtimeConnectionsLimitMax)))
		return
	}
	connectionIDs, problem := managedRealtimeDrainConnectionIDs(request.ConnectionIDs)
	if problem != nil {
		api.WriteProblem(w, problem)
		return
	}
	inventory, err := lister.ListConnectionInventory(r.Context())
	if err != nil && inventory.NodesQueried == 0 {
		api.WriteProblem(w, api.ErrCapacity("managed realtime owner unavailable"))
		return
	}
	partial := inventory.NodesUnavailable > 0
	if partial && !request.DryRun && !request.AllowPartial {
		api.WriteProblem(w, api.NewProblem(http.StatusConflict, api.CodeConflict, "Realtime inventory is partial", "retry after all realtime nodes are reachable or set allow_partial=true to drain the reachable subset"))
		return
	}
	selected, truncated := managedRealtimeDrainCandidates(inventory.Connections, row, acct, request, connectionIDs)
	operationStore, ok := s.store.(state.ManagedRealtimeDrainOperationStore)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("managed realtime drain operation store unavailable"))
		return
	}
	operation, err := operationStore.CreateManagedRealtimeDrainOperation(r.Context(), state.ManagedRealtimeDrainOperationInput{
		AccountID: acct.ID, AppID: row.AppID, EndpointID: row.ID, Reason: request.Reason,
		DryRun: request.DryRun, Matched: len(selected),
	})
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not persist managed realtime drain operation"))
		return
	}
	response := api.ManagedRealtimeDrainResponse{
		OperationID:      operation.ID,
		Status:           string(operation.Status),
		CreatedAt:        api.FormatAlertTime(operation.CreatedAt),
		Results:          make([]api.ManagedRealtimeDrainResult, 0, len(selected)),
		Matched:          len(selected),
		Limit:            request.Limit,
		Truncated:        truncated,
		DryRun:           request.DryRun,
		Partial:          partial,
		NodesQueried:     inventory.NodesQueried,
		NodesUnavailable: inventory.NodesUnavailable,
	}
	for _, connection := range selected {
		if request.DryRun {
			response.Results = append(response.Results, api.ManagedRealtimeDrainResult{ID: connection.ID, Status: managedRealtimeDrainStatusWouldClose})
			continue
		}
		err := owner.CloseConnection(r.Context(), row.ID, connection.ID, request.Reason)
		status := managedRealtimeDrainStatusClosed
		switch {
		case err == nil:
			response.Closed++
		case managedRealtimeConnectionGone(err):
			status = managedRealtimeDrainStatusGone
			response.Gone++
		default:
			status = managedRealtimeDrainStatusFailed
			response.Failed++
			s.log.WarnContext(r.Context(), "drain managed realtime connection", "endpoint_id", row.ID, "connection_id", connection.ID, "err", err)
		}
		response.Results = append(response.Results, api.ManagedRealtimeDrainResult{ID: connection.ID, Status: status})
	}
	auditKind := "realtime.connections_drained"
	if request.DryRun {
		auditKind = "realtime.connections_drain_previewed"
	}
	auditData := map[string]any{
		"endpoint_id": row.ID,
		"matched":     response.Matched,
		"closed":      response.Closed,
		"gone":        response.Gone,
		"failed":      response.Failed,
		"dry_run":     request.DryRun,
		"partial":     response.Partial,
		"truncated":   response.Truncated,
		"reason":      request.Reason,
	}
	if request.Channel != "" {
		auditData["channel"] = request.Channel
	}
	if request.Principal != "" {
		auditData["principal"] = request.Principal
	}
	if len(connectionIDs) > 0 {
		auditData["connection_ids"] = len(connectionIDs)
	}
	response.Status = string(state.ManagedRealtimeDrainOperationCompleted)
	if response.Gone > 0 || response.Failed > 0 {
		response.Status = string(state.ManagedRealtimeDrainOperationPartial)
	}
	result, err := json.Marshal(response)
	if err != nil {
		s.log.WarnContext(r.Context(), "encode managed realtime drain operation result", "operation_id", operation.ID, "err", err)
	} else if completed, completeErr := operationStore.CompleteManagedRealtimeDrainOperation(r.Context(), operation.ID, acct.ID, row.ID, state.ManagedRealtimeDrainOperationStatus(response.Status), result, response.Matched, response.Closed, response.Gone, response.Failed); completeErr != nil {
		s.log.WarnContext(r.Context(), "complete managed realtime drain operation", "operation_id", operation.ID, "err", completeErr)
	} else if completed.CompletedAt != nil {
		completedAt := api.FormatAlertTime(*completed.CompletedAt)
		response.CompletedAt = &completedAt
	}
	auditData["operation_id"] = operation.ID
	auditData["status"] = response.Status
	s.audit.Emit(r.Context(), auditKind, &acct.ID, auditData)
	writeJSON(w, http.StatusOK, response)
}

func (s *server) getManagedRealtimeDrainOperation(w http.ResponseWriter, r *http.Request, acct state.Account) {
	row, _, ok := s.loadManagedRealtimeEndpoint(w, r, acct)
	if !ok {
		return
	}
	operationStore, ok := s.store.(state.ManagedRealtimeDrainOperationStore)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("managed realtime drain operation store unavailable"))
		return
	}
	operation, err := operationStore.GetManagedRealtimeDrainOperation(r.Context(), acct.ID, row.ID, strings.TrimSpace(r.PathValue("drain_id")))
	if err != nil {
		if errors.Is(err, state.ErrManagedRealtimeDrainOperationNotFound) {
			s.notFound(w, "managed realtime drain operation not found")
			return
		}
		api.WriteProblem(w, api.ErrCapacity("could not read managed realtime drain operation"))
		return
	}
	if operation.AppID != row.AppID {
		s.notFound(w, "managed realtime drain operation not found")
		return
	}
	writeJSON(w, http.StatusOK, managedRealtimeDrainResponseFromOperation(operation))
}

func managedRealtimeDrainResponseFromOperation(operation state.ManagedRealtimeDrainOperation) api.ManagedRealtimeDrainResponse {
	var response api.ManagedRealtimeDrainResponse
	if len(operation.Result) > 0 && string(operation.Result) != "{}" {
		_ = json.Unmarshal(operation.Result, &response)
	}
	response.OperationID = operation.ID
	response.Status = string(operation.Status)
	response.CreatedAt = api.FormatAlertTime(operation.CreatedAt)
	response.Matched = operation.Matched
	response.Closed = operation.Closed
	response.Gone = operation.Gone
	response.Failed = operation.Failed
	response.DryRun = operation.DryRun
	if response.CompletedAt == nil && operation.CompletedAt != nil {
		completedAt := api.FormatAlertTime(*operation.CompletedAt)
		response.CompletedAt = &completedAt
	}
	if response.Results == nil {
		response.Results = []api.ManagedRealtimeDrainResult{}
	}
	return response
}

func managedRealtimeDrainConnectionIDs(values []string) (map[string]struct{}, *api.Problem) {
	if len(values) > managedRealtimeDrainConnectionIDsMax {
		return nil, api.ErrRealtimeInvalid(fmt.Sprintf("connection_ids cannot contain more than %d values", managedRealtimeDrainConnectionIDsMax))
	}
	if len(values) == 0 {
		return nil, nil
	}
	ids := make(map[string]struct{}, len(values))
	for _, value := range values {
		id := strings.TrimSpace(value)
		if id == "" {
			return nil, api.ErrRealtimeInvalid("connection_ids cannot contain an empty value")
		}
		if len(id) > 256 {
			return nil, api.ErrRealtimeInvalid("connection id exceeds 256 bytes")
		}
		if _, exists := ids[id]; exists {
			return nil, api.ErrRealtimeInvalid("connection_ids cannot contain duplicates")
		}
		ids[id] = struct{}{}
	}
	return ids, nil
}

func managedRealtimeDrainCandidates(connections []realtime.ConnectionInfo, row state.ManagedRealtimeEndpoint, acct state.Account, request api.ManagedRealtimeDrainRequest, connectionIDs map[string]struct{}) ([]realtime.ConnectionInfo, bool) {
	connections = append([]realtime.ConnectionInfo(nil), connections...)
	sort.Slice(connections, func(i, j int) bool { return connections[i].ID < connections[j].ID })
	selected := make([]realtime.ConnectionInfo, 0, min(managedRealtimeConnectionsLimitMax, len(connections)))
	seen := make(map[string]struct{}, len(connections))
	truncated := false
	for _, connection := range connections {
		if _, exists := seen[connection.ID]; exists {
			continue
		}
		seen[connection.ID] = struct{}{}
		if connection.EndpointID != row.ID || connection.AppID != row.AppID || connection.AccountID != acct.ID {
			continue
		}
		if len(connectionIDs) > 0 {
			if _, exists := connectionIDs[connection.ID]; !exists {
				continue
			}
		}
		if request.Channel != "" && !realtimeConnectionHasChannel(connection, request.Channel) {
			continue
		}
		if request.Principal != "" && connection.Principal != request.Principal {
			continue
		}
		if len(selected) == request.Limit {
			truncated = true
			continue
		}
		selected = append(selected, connection)
	}
	return selected, truncated
}

func managedRealtimeConnectionGone(err error) bool {
	if errors.Is(err, realtime.ErrConnectionNotFound) || errors.Is(err, realtime.ErrConnectionClosed) {
		return true
	}
	var managementErr *realtime.ManagementError
	return errors.As(err, &managementErr) && (managementErr.StatusCode == http.StatusNotFound || managementErr.StatusCode == http.StatusGone)
}

func (s *server) sendManagedRealtimeConnection(w http.ResponseWriter, r *http.Request, acct state.Account) {
	row, owner, ok := s.managedRealtimeEndpointAction(w, r, acct)
	if !ok {
		return
	}
	message, problem := decodeManagedRealtimeMessage(r)
	if problem != nil {
		api.WriteProblem(w, problem)
		return
	}
	connectionID := strings.TrimSpace(r.PathValue("connection_id"))
	if connectionID == "" {
		api.WriteProblem(w, api.ErrRealtimeInvalid("connection_id is required"))
		return
	}
	if err := owner.Send(r.Context(), row.ID, connectionID, message); err != nil {
		s.writeManagedRealtimeOwnerError(w, r, "send message", err)
		return
	}
	s.audit.Emit(r.Context(), "realtime.message_sent", &acct.ID, map[string]any{
		"endpoint_id": row.ID, "connection_id": connectionID,
	})
	w.WriteHeader(http.StatusAccepted)
}

func (s *server) closeManagedRealtimeConnection(w http.ResponseWriter, r *http.Request, acct state.Account) {
	row, owner, ok := s.managedRealtimeEndpointAction(w, r, acct)
	if !ok {
		return
	}
	reason := "management API"
	if r.Body != nil && r.ContentLength != 0 {
		var request api.ManagedRealtimeCloseRequest
		if err := decodeJSON(r, &request); err != nil {
			api.WriteProblem(w, api.ErrRealtimeInvalid(err.Error()))
			return
		}
		if len(request.Reason) > 256 {
			api.WriteProblem(w, api.ErrRealtimeInvalid("reason exceeds 256 bytes"))
			return
		}
		if strings.TrimSpace(request.Reason) != "" {
			reason = strings.TrimSpace(request.Reason)
		}
	}
	connectionID := strings.TrimSpace(r.PathValue("connection_id"))
	if connectionID == "" {
		api.WriteProblem(w, api.ErrRealtimeInvalid("connection_id is required"))
		return
	}
	if err := owner.CloseConnection(r.Context(), row.ID, connectionID, reason); err != nil {
		s.writeManagedRealtimeOwnerError(w, r, "close connection", err)
		return
	}
	s.audit.Emit(r.Context(), "realtime.connection_closed", &acct.ID, map[string]any{
		"endpoint_id": row.ID, "connection_id": connectionID,
	})
	w.WriteHeader(http.StatusNoContent)
}

func (s *server) subscribeManagedRealtimeConnection(w http.ResponseWriter, r *http.Request, acct state.Account) {
	s.manageRealtimeSubscription(w, r, acct, true)
}

func (s *server) unsubscribeManagedRealtimeConnection(w http.ResponseWriter, r *http.Request, acct state.Account) {
	s.manageRealtimeSubscription(w, r, acct, false)
}

func (s *server) manageRealtimeSubscription(w http.ResponseWriter, r *http.Request, acct state.Account, subscribe bool) {
	row, owner, ok := s.managedRealtimeEndpointAction(w, r, acct)
	if !ok {
		return
	}
	channel := r.PathValue("channel")
	if problem := validateManagedRealtimeChannel(channel); problem != nil {
		api.WriteProblem(w, problem)
		return
	}
	connectionID := strings.TrimSpace(r.PathValue("connection_id"))
	if connectionID == "" {
		api.WriteProblem(w, api.ErrRealtimeInvalid("connection_id is required"))
		return
	}
	var err error
	if subscribe {
		err = owner.Subscribe(r.Context(), row.ID, connectionID, channel)
	} else {
		err = owner.Unsubscribe(r.Context(), row.ID, connectionID, channel)
	}
	if err != nil {
		s.writeManagedRealtimeOwnerError(w, r, "update subscription", err)
		return
	}
	s.audit.Emit(r.Context(), "realtime.subscription_updated", &acct.ID, map[string]any{
		"endpoint_id": row.ID, "connection_id": connectionID, "channel": channel, "subscribed": subscribe,
	})
	w.WriteHeader(http.StatusNoContent)
}

func (s *server) publishManagedRealtimeChannel(w http.ResponseWriter, r *http.Request, acct state.Account) {
	row, owner, ok := s.managedRealtimeEndpointAction(w, r, acct)
	if !ok {
		return
	}
	channel := r.PathValue("channel")
	if problem := validateManagedRealtimeChannel(channel); problem != nil {
		api.WriteProblem(w, problem)
		return
	}
	message, problem := decodeManagedRealtimeMessage(r)
	if problem != nil {
		api.WriteProblem(w, problem)
		return
	}
	queued, err := owner.Publish(r.Context(), row.ID, channel, message)
	if err != nil {
		s.writeManagedRealtimeOwnerError(w, r, "publish message", err)
		return
	}
	s.audit.Emit(r.Context(), "realtime.channel_published", &acct.ID, map[string]any{
		"endpoint_id": row.ID, "channel": channel, "queued": queued,
	})
	writeJSON(w, http.StatusOK, api.ManagedRealtimePublishResponse{Queued: queued})
}

func (s *server) writeManagedRealtimeOwnerError(w http.ResponseWriter, r *http.Request, operation string, err error) {
	var managementErr *realtime.ManagementError
	if errors.As(err, &managementErr) {
		switch managementErr.StatusCode {
		case http.StatusGone:
			api.WriteProblem(w, api.NewProblem(http.StatusGone, api.CodeNotFound, "Realtime connection unavailable", "the connection is no longer live on its owner node"))
			return
		case http.StatusNotFound:
			// A durable endpoint that is absent from this local daemon is an
			// owner-routing miss, not a customer-visible 404. The cross-node
			// resolver will retry or redirect this case once enabled.
			if operation == "publish message" {
				s.log.WarnContext(r.Context(), "managed realtime endpoint owner not found", "err", err)
				api.WriteProblem(w, api.ErrCapacity("managed realtime owner unavailable"))
				return
			}
			api.WriteProblem(w, api.NewProblem(http.StatusGone, api.CodeNotFound, "Realtime connection unavailable", "the connection is no longer live on its owner node"))
			return
		case http.StatusTooManyRequests:
			api.WriteProblem(w, api.NewProblem(http.StatusTooManyRequests, api.CodeCapacity, "Realtime queue full", "the connection owner is temporarily unable to accept more messages"))
			return
		}
	}
	if errors.Is(err, realtime.ErrConnectionNotFound) || errors.Is(err, realtime.ErrConnectionClosed) {
		api.WriteProblem(w, api.NewProblem(http.StatusGone, api.CodeNotFound, "Realtime connection unavailable", "the connection is no longer live on its owner node"))
		return
	}
	if errors.Is(err, realtime.ErrOutboundQueueFull) || errors.Is(err, realtime.ErrTooManyConnections) {
		api.WriteProblem(w, api.NewProblem(http.StatusTooManyRequests, api.CodeCapacity, "Realtime queue full", "the connection owner is temporarily unable to accept more messages"))
		return
	}
	s.log.WarnContext(r.Context(), "managed realtime owner operation failed", "operation", operation, "err", err)
	api.WriteProblem(w, api.ErrCapacity("managed realtime owner unavailable"))
}
