package main

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
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
