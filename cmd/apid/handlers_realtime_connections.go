package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
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

type realtimePublishStatus interface {
	PublishWithStatus(context.Context, string, string, realtime.Message) (api.ManagedRealtimePublishResponse, error)
}

type realtimeRetainedPublishStatus interface {
	PublishRetainedWithStatus(context.Context, string, string, realtime.Message, int64) (api.ManagedRealtimePublishResponse, error)
}

type realtimeConnectionInventory interface {
	ListConnectionInventory(context.Context) (realtime.ConnectionInventory, error)
}

const (
	managedRealtimeConnectionsLimitDefault      = 100
	managedRealtimeConnectionsLimitMax          = 1000
	managedRealtimeDrainConnectionIDsMax        = 100
	managedRealtimeDrainAllMax                  = 10000
	managedRealtimePublishIdempotencyMaxBytes   = 128
	managedRealtimePublishIdempotencySweepEvery = 1024
	managedRealtimePublishIdempotencySweepBatch = 4096
)

func managedRealtimeDeliveryFromRequest(r *http.Request) (api.ManagedRealtimeDelivery, bool) {
	query, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		return "", false
	}
	values := query["delivery"]
	if len(values) == 0 {
		return api.ManagedRealtimeDeliveryLive, true
	}
	if len(values) != 1 {
		return "", false
	}
	delivery := api.ManagedRealtimeDelivery(values[0])
	switch delivery {
	case api.ManagedRealtimeDeliveryLive, api.ManagedRealtimeDeliveryRetained:
		return delivery, true
	default:
		return "", false
	}
}

func managedRealtimePublishFingerprint(r *http.Request) ([32]byte, bool) {
	var zero [32]byte
	delivery, validDelivery := managedRealtimeDeliveryFromRequest(r)
	if !validDelivery {
		return zero, false
	}
	if r.Body == nil {
		return zero, false
	}
	const maxRequestBytes = 2 << 20
	originalBody := r.Body
	body, err := io.ReadAll(io.LimitReader(originalBody, maxRequestBytes+1))
	r.Body = io.NopCloser(io.MultiReader(bytes.NewReader(body), originalBody))
	if err != nil || len(body) > maxRequestBytes {
		return zero, false
	}
	probe := r.Clone(r.Context())
	probe.Body = io.NopCloser(bytes.NewReader(body))
	message, problem := decodeManagedRealtimeMessage(probe)
	if problem != nil {
		return zero, false
	}
	// Keep the original live fingerprint so a rolling deployment can replay
	// existing live receipts. Retained delivery gets a distinct domain prefix.
	canonical := make([]byte, 0, len("retained\x00")+1+len(message.Data))
	if delivery == api.ManagedRealtimeDeliveryRetained {
		canonical = append(canonical, "retained\x00"...)
	}
	messageOffset := len(canonical)
	canonical = append(canonical, 0)
	if message.Binary {
		canonical[messageOffset] = 1
	}
	canonical = append(canonical, message.Data...)
	return sha256.Sum256(canonical), true
}

// idempotentManagedRealtimePublish binds a stable publish key to the decoded
// payload. An in-flight publish is not run a second time while its outcome
// could be unknown after a control-plane crash.
func (s *server) idempotentManagedRealtimePublish(next accountHandler) accountHandler {
	return func(w http.ResponseWriter, r *http.Request, acct state.Account) {
		keys := r.Header.Values("Idempotency-Key")
		if len(keys) > 1 {
			api.WriteProblem(w, api.ErrRealtimeInvalid("send exactly one Idempotency-Key header"))
			return
		}
		key := ""
		if len(keys) == 1 {
			key = keys[0]
		}
		if key == "" {
			next(w, r, acct)
			return
		}
		if len(key) > managedRealtimePublishIdempotencyMaxBytes ||
			strings.IndexFunc(key, func(r rune) bool { return r < 0x21 || r > 0x7e }) >= 0 {
			api.WriteProblem(w, api.ErrRealtimeInvalid("Idempotency-Key must contain 1 to 128 printable ASCII bytes with no whitespace"))
			return
		}
		fingerprint, validPayload := managedRealtimePublishFingerprint(r)
		if !validPayload {
			// Let the endpoint handler return its normal bounded-body or JSON
			// validation problem without consuming an idempotency key.
			next(w, r, acct)
			return
		}
		store, ok := s.store.(state.ManagedRealtimePublishIdempotencyStore)
		if !ok {
			api.WriteProblem(w, api.ErrCapacity("managed realtime publish idempotency unavailable"))
			return
		}
		// Sweep four times the observed keyed request rate in a bounded batch,
		// keeping expired receipts from accumulating under sustained publishing.
		if s.managedRealtimePublishIdempotencySweep.Add(1)%managedRealtimePublishIdempotencySweepEvery == 0 {
			if reaper, ok := s.store.(state.ManagedRealtimePublishIdempotencyReaper); ok {
				if _, err := reaper.ReapManagedRealtimePublishIdempotency(r.Context(), managedRealtimePublishIdempotencySweepBatch); err != nil && s.log != nil && !errors.Is(err, context.Canceled) {
					s.log.WarnContext(r.Context(), "managed realtime publish idempotency cleanup failed", "error", err)
				}
			}
		}
		scopedKey := "managed-realtime-publish\n" + r.Method + " " + r.URL.EscapedPath() + "\n" + key
		reservation, err := store.ReserveManagedRealtimePublish(r.Context(), acct.ID, scopedKey, fingerprint[:])
		if err != nil {
			if s.log != nil {
				s.log.WarnContext(r.Context(), "managed realtime publish idempotency reservation failed", "error", err)
			}
			api.WriteProblem(w, api.ErrCapacity("managed realtime publish idempotency unavailable"))
			return
		}
		if reservation.Conflict {
			api.WriteProblem(w, api.NewProblem(http.StatusConflict, api.CodeConflict,
				"Idempotency key conflict", "this Idempotency-Key was already used with a different realtime message"))
			return
		}
		if reservation.InFlight {
			api.WriteProblem(w, api.NewProblem(http.StatusConflict, api.CodeConflict,
				"Publish outcome pending", "a publish with this Idempotency-Key is still in progress or its outcome is unknown; retry the same request later").
				WithHeader("Retry-After", "1"))
			return
		}
		if !reservation.Reserved {
			replayManagedRealtimePublish(w, reservation.Status, reservation.Body)
			return
		}

		cap := &captureWriter{ResponseWriter: w, status: http.StatusOK}
		next(cap, r, acct)
		if managedRealtimePublishReplayable(cap.status) {
			if err := s.store.PutIdempotent(context.WithoutCancel(r.Context()), acct.ID, scopedKey, cap.status, cap.body.Bytes()); err != nil {
				// Keep the reservation in-flight: a repeat must not publish again
				// when the first outcome could not be durably recorded.
				if s.log != nil {
					s.log.WarnContext(r.Context(), "managed realtime publish receipt persistence failed", "error", err)
				}
			}
			return
		}
		if reserver, ok := s.store.(idempotencyReserver); ok {
			_ = reserver.ReleaseIdempotent(context.WithoutCancel(r.Context()), acct.ID, scopedKey)
		}
	}
}

func replayManagedRealtimePublish(w http.ResponseWriter, status int, body []byte) {
	contentType := "application/json"
	if status >= 400 {
		contentType = "application/problem+json"
	}
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Idempotent-Replayed", "true")
	w.WriteHeader(status)
	_, _ = w.Write(body)
}

func managedRealtimePublishReplayable(status int) bool {
	// A 5xx or rate-limit response can follow an ambiguous owner RPC. Replay
	// that result rather than risk enqueueing a message twice.
	return status >= 200 && status < 300 || status == http.StatusTooManyRequests || status >= 500
}

type managedRealtimeConnectionCursor struct {
	Version   int    `json:"v"`
	AfterID   string `json:"after_id"`
	Channel   string `json:"channel,omitempty"`
	Principal string `json:"principal,omitempty"`
}

func encodeManagedRealtimeConnectionCursor(channel, principal, afterID string) (string, error) {
	payload, err := json.Marshal(managedRealtimeConnectionCursor{
		Version: 1, AfterID: afterID, Channel: channel, Principal: principal,
	})
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(payload), nil
}

func decodeManagedRealtimeConnectionCursor(raw, channel, principal string) (string, error) {
	if raw == "" {
		return "", nil
	}
	payload, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil {
		return "", fmt.Errorf("cursor is not valid base64url")
	}
	var cursor managedRealtimeConnectionCursor
	if err := json.Unmarshal(payload, &cursor); err != nil || cursor.Version != 1 || cursor.AfterID == "" {
		return "", fmt.Errorf("cursor is invalid or expired")
	}
	if cursor.Channel != channel || cursor.Principal != principal {
		return "", fmt.Errorf("cursor does not match the requested filters")
	}
	return cursor.AfterID, nil
}

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

func (o localRealtimeOwner) UnsubscribeWithRouteState(ctx context.Context, endpointID, connectionID, channel string) (bool, bool, error) {
	if err := o.ownsConnection(ctx, endpointID, connectionID); err != nil {
		return false, false, err
	}
	return o.client.UnsubscribeWithRouteState(ctx, connectionID, channel)
}

func (o localRealtimeOwner) Publish(ctx context.Context, endpointID, channel string, message realtime.Message) (int, error) {
	return o.client.Publish(ctx, endpointID, channel, message)
}

func (o localRealtimeOwner) PublishWithStatus(ctx context.Context, endpointID, channel string, message realtime.Message) (api.ManagedRealtimePublishResponse, error) {
	status, err := o.client.PublishWithStatus(ctx, endpointID, channel, message)
	return api.ManagedRealtimePublishResponse{
		Queued: status.Queued, Subscribers: status.Subscribers, QueueFull: status.QueueFull, Failed: status.Failed,
		NodesQueried: 1, Partial: status.QueueFull > 0 || status.Failed > 0,
	}, err
}

func (o localRealtimeOwner) PublishRetainedWithStatus(ctx context.Context, endpointID, channel string, message realtime.Message, sequence int64) (api.ManagedRealtimePublishResponse, error) {
	status, err := o.client.PublishRetainedWithStatus(ctx, endpointID, channel, message, sequence)
	var managementErr *realtime.ManagementError
	if errors.As(err, &managementErr) && (managementErr.StatusCode == http.StatusBadRequest || managementErr.StatusCode == http.StatusNotFound) {
		return o.PublishWithStatus(ctx, endpointID, channel, message)
	}
	return api.ManagedRealtimePublishResponse{
		Queued: status.Queued, Subscribers: status.Subscribers, QueueFull: status.QueueFull, Failed: status.Failed,
		NodesQueried: 1, Partial: status.QueueFull > 0 || status.Failed > 0,
	}, err
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
	principal := strings.TrimSpace(r.URL.Query().Get("principal"))
	if len(principal) > 256 {
		api.WriteProblem(w, api.ErrRealtimeInvalid("principal exceeds 256 bytes"))
		return
	}
	afterID, err := decodeManagedRealtimeConnectionCursor(r.URL.Query().Get("cursor"), channel, principal)
	if err != nil {
		api.WriteProblem(w, api.ErrRealtimeInvalid(err.Error()))
		return
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
		if principal != "" && connection.Principal != principal {
			continue
		}
		if afterID != "" && connection.ID <= afterID {
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
	nextCursor := ""
	if truncated && len(connections) > 0 {
		nextCursor, err = encodeManagedRealtimeConnectionCursor(channel, principal, connections[len(connections)-1].ID)
		if err != nil {
			api.WriteProblem(w, api.ErrCapacity("could not encode realtime connection cursor"))
			return
		}
	}
	writeJSON(w, http.StatusOK, api.ManagedRealtimeConnectionListResponse{
		Connections:      connections,
		Limit:            limit,
		Truncated:        truncated,
		NextCursor:       nextCursor,
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
	managedRealtimeDrainStatusPending    = "pending"
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
	if request.All {
		if len(request.ConnectionIDs) > 0 {
			api.WriteProblem(w, api.ErrRealtimeInvalid("all cannot be combined with connection_ids"))
			return
		}
		request.Limit = managedRealtimeDrainAllMax
	} else if request.Limit == 0 {
		request.Limit = managedRealtimeConnectionsLimitDefault
	}
	if !request.All && (request.Limit < 1 || request.Limit > managedRealtimeConnectionsLimitMax) {
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
	if request.All && truncated {
		api.WriteProblem(w, api.NewProblem(http.StatusConflict, api.CodeConflict, "Realtime drain selection exceeds the safety cap", fmt.Sprintf("all mode is limited to %d connections; narrow the channel or principal filter", managedRealtimeDrainAllMax)))
		return
	}
	operationStore, ok := s.store.(state.ManagedRealtimeDrainOperationStore)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("managed realtime drain operation store unavailable"))
		return
	}
	operation, err := operationStore.CreateManagedRealtimeDrainOperation(r.Context(), state.ManagedRealtimeDrainOperationInput{
		AccountID: acct.ID, AppID: row.AppID, EndpointID: row.ID, Reason: request.Reason,
		DryRun: request.DryRun, Matched: len(selected),
		ConnectionIDs: managedRealtimeDrainCandidateIDs(selected), Limit: request.Limit, All: request.All,
		Truncated: truncated, Partial: partial, NodesQueried: inventory.NodesQueried,
		NodesUnavailable: inventory.NodesUnavailable,
	})
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not persist managed realtime drain operation"))
		return
	}
	response := managedRealtimeDrainResponseFromOperation(operation)
	response.Results = make([]api.ManagedRealtimeDrainResult, 0, len(selected))
	for _, connection := range selected {
		response.Results = append(response.Results, api.ManagedRealtimeDrainResult{ID: connection.ID, Status: managedRealtimeDrainStatusPending})
	}
	// Detach execution from the request context. The durable worker owns
	// retries and resumes running rows after an apid restart.
	s.wakeManagedRealtimeDrainWorker()
	if worker, ok := s.store.(state.ManagedRealtimeDrainOperationWorker); ok {
		go s.runManagedRealtimeDrainPass(context.WithoutCancel(r.Context()), worker)
	}
	writeJSON(w, http.StatusAccepted, response)
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
	response := managedRealtimeDrainResponseForExecution(operation)
	response.Closed = operation.Closed
	response.Gone = operation.Gone
	response.Failed = operation.Failed
	response.All = operation.All
	if response.CompletedAt == nil && operation.CompletedAt != nil {
		completedAt := api.FormatAlertTime(*operation.CompletedAt)
		response.CompletedAt = &completedAt
	}
	if response.Results == nil {
		response.Results = []api.ManagedRealtimeDrainResult{}
	}
	for _, connectionID := range operation.ConnectionIDs {
		response.Results = append(response.Results, api.ManagedRealtimeDrainResult{ID: connectionID, Status: managedRealtimeDrainStatusPending})
	}
	return response
}

func managedRealtimeDrainCandidateIDs(connections []realtime.ConnectionInfo) []string {
	ids := make([]string, 0, len(connections))
	for _, connection := range connections {
		ids = append(ids, connection.ID)
	}
	return ids
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
	selected := make([]realtime.ConnectionInfo, 0, min(managedRealtimeDrainAllMax, len(connections)))
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
	delivery, validDelivery := managedRealtimeDeliveryFromRequest(r)
	if !validDelivery {
		api.WriteProblem(w, api.ErrRealtimeInvalid("delivery must be live or retained, and may be specified once"))
		return
	}
	var retainedSequence int64
	if delivery == api.ManagedRealtimeDeliveryRetained {
		if !s.realtimeHistoryPreviewEnabled {
			s.notFound(w, "retained realtime publishing unavailable")
			return
		}
		limits, allowed := s.realtimePlanLimits(acct)
		if !allowed {
			api.WriteProblem(w, api.ErrPlanRealtimeNotAllowed(acct.Plan))
			return
		}
		keys := r.Header.Values("Idempotency-Key")
		if len(keys) != 1 || keys[0] == "" {
			api.WriteProblem(w, api.ErrRealtimeInvalid("delivery=retained requires exactly one Idempotency-Key header"))
			return
		}
		if len(message.Data) > state.ManagedRealtimeHistoryMaxPayloadBytes {
			api.WriteProblem(w, api.ErrRealtimeInvalid("retained messages are limited to 4096 decoded bytes"))
			return
		}
		if row.MaxMessageBytes > 0 && int64(len(message.Data)) > row.MaxMessageBytes {
			api.WriteProblem(w, api.ErrRealtimeInvalid("message exceeds this endpoint's max_message_bytes"))
			return
		}
		store, ok := s.managedRealtimeHistoryQuotaStore(w)
		if !ok {
			return
		}
		retained, err := store.AppendManagedRealtimeChannelMessageWithQuota(r.Context(), acct.ID, row.ID, channel, message.Data, message.Binary, keys[0], limits.RetainedHistoryMaxPayloadBytesPerAccount)
		if err != nil {
			s.writeManagedRealtimeHistoryError(w, r, acct, err)
			return
		}
		retainedSequence = retained.Sequence
	}
	result := api.ManagedRealtimePublishResponse{NodesQueried: 1}
	var err error
	if delivery == api.ManagedRealtimeDeliveryRetained {
		if publisher, ok := owner.(realtimeRetainedPublishStatus); ok {
			result, err = publisher.PublishRetainedWithStatus(r.Context(), row.ID, channel, message, retainedSequence)
		} else if publisher, ok := owner.(realtimePublishStatus); ok {
			result, err = publisher.PublishWithStatus(r.Context(), row.ID, channel, message)
		} else {
			result.Queued, err = owner.Publish(r.Context(), row.ID, channel, message)
			result.Subscribers = result.Queued
		}
		// The ordered history commit is authoritative. If immediate live fanout
		// is unavailable, resume subscribers can still recover from the log.
		result.Sequence = retainedSequence
		result.Durable = true
		result.Partial = result.Partial || result.NodesUnavailable > 0 || result.QueueFull > 0 || result.Failed > 0 || err != nil
		if err != nil {
			s.log.WarnContext(r.Context(), "retained realtime publish committed but live fanout was incomplete", "endpoint_id", row.ID, "channel", channel, "sequence", retainedSequence, "err", err)
		}
	} else if publisher, ok := owner.(realtimePublishStatus); ok {
		result, err = publisher.PublishWithStatus(r.Context(), row.ID, channel, message)
	} else {
		result.Queued, err = owner.Publish(r.Context(), row.ID, channel, message)
		result.Subscribers = result.Queued
	}
	result.Partial = result.Partial || result.NodesUnavailable > 0 || result.QueueFull > 0 || result.Failed > 0
	if err != nil && delivery != api.ManagedRealtimeDeliveryRetained {
		s.writeManagedRealtimeOwnerError(w, r, "publish message", err)
		return
	}
	s.audit.Emit(r.Context(), "realtime.channel_published", &acct.ID, map[string]any{
		"endpoint_id": row.ID, "channel": channel, "queued": result.Queued,
		"subscribers": result.Subscribers, "queue_full": result.QueueFull,
		"failed": result.Failed, "partial": result.Partial, "nodes_unavailable": result.NodesUnavailable,
		"delivery": delivery, "sequence": result.Sequence, "durable": result.Durable,
	})
	writeJSON(w, http.StatusOK, result)
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
