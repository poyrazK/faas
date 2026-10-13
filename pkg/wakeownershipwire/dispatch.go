// Package wakeownershipwire carries retained dispatch provenance over the
// private authenticated schedd-to-vmmd channel. It does not authenticate peers
// or grant launch authority; native ownership must still be durably bound.
// adr: 590
package wakeownershipwire

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/state"
	"google.golang.org/grpc/metadata"
)

const prefix = "x-faas-checkpoint-wake-"

func validUUID(value string) bool {
	id, err := uuid.Parse(value)
	return err == nil && id != uuid.Nil && id.String() == value
}

func validate(frame state.ProjectEnvironmentCloneWakeDispatch) error {
	for _, id := range []string{frame.InstanceID, frame.AppID, frame.DeploymentID, frame.NodeID, frame.WakeID} {
		if !validUUID(id) {
			return fmt.Errorf("checkpoint wake transport: invalid original identity: %w", state.ErrInvalidArgument)
		}
	}
	operation, ok := strings.CutPrefix(frame.RequestReason, "environment-clone:")
	if !ok || !validUUID(operation) || (frame.Method != "cold_boot" && frame.Method != "restore") || frame.DispatchedAt.IsZero() || frame.DispatchedAt.Year() < 1 || frame.DispatchedAt.Year() > 9999 {
		return fmt.Errorf("checkpoint wake transport: incomplete original dispatch: %w", state.ErrInvalidArgument)
	}
	return nil
}

func fields(frame state.ProjectEnvironmentCloneWakeDispatch) map[string]string {
	return map[string]string{
		"version": "1", "instance": frame.InstanceID, "app": frame.AppID,
		"deployment": frame.DeploymentID, "node": frame.NodeID, "wake": frame.WakeID,
		"reason": frame.RequestReason, "method": frame.Method,
		"dispatched-at": frame.DispatchedAt.UTC().Format(time.RFC3339Nano),
	}
}

// Outgoing requires a retained original record, never one reconstructed from
// tracing metadata or current instance state. Existing envelopes are rejected
// rather than overwritten. Authentication metadata is preserved.
func Outgoing(ctx context.Context, frame state.ProjectEnvironmentCloneWakeDispatch) (context.Context, error) {
	if err := validate(frame); err != nil {
		return nil, err
	}
	md, _ := metadata.FromOutgoingContext(ctx)
	md = md.Copy()
	for key := range md {
		if strings.HasPrefix(key, prefix) {
			return nil, fmt.Errorf("checkpoint wake transport: existing envelope: %w", state.ErrInvalidArgument)
		}
	}
	for key, value := range fields(frame) {
		md.Set(prefix+key, value)
	}
	return metadata.NewOutgoingContext(ctx, md), nil
}

// Incoming requires prior private peer authentication and vmmd's configured
// local node. Missing, duplicate and unknown envelope fields fail closed; no
// ordinary correlation header can substitute for original ownership. A decoded
// record is provenance only, not a replay permit or producer capability.
func Incoming(ctx context.Context, localNodeID string) (state.ProjectEnvironmentCloneWakeDispatch, error) {
	var zero state.ProjectEnvironmentCloneWakeDispatch
	if !validUUID(localNodeID) {
		return zero, fmt.Errorf("checkpoint wake transport: local node unavailable: %w", state.ErrConflict)
	}
	md, _ := metadata.FromIncomingContext(ctx)
	values := fields(zero)
	for key, entries := range md {
		name, owned := strings.CutPrefix(key, prefix)
		if !owned {
			continue
		}
		if _, known := values[name]; !known || len(entries) != 1 || entries[0] == "" {
			return zero, fmt.Errorf("checkpoint wake transport: ambiguous envelope: %w", state.ErrInvalidArgument)
		}
	}
	for key := range values {
		entries := md.Get(prefix + key)
		if len(entries) != 1 || entries[0] == "" {
			return zero, fmt.Errorf("checkpoint wake transport: incomplete envelope: %w", state.ErrInvalidArgument)
		}
		values[key] = entries[0]
	}
	if values["version"] != "1" {
		return zero, fmt.Errorf("checkpoint wake transport: unsupported version: %w", state.ErrInvalidArgument)
	}
	at, err := time.Parse(time.RFC3339Nano, values["dispatched-at"])
	if err != nil || at.UTC().Format(time.RFC3339Nano) != values["dispatched-at"] {
		return zero, fmt.Errorf("checkpoint wake transport: invalid dispatch timestamp: %w", state.ErrInvalidArgument)
	}
	frame := state.ProjectEnvironmentCloneWakeDispatch{
		InstanceID: values["instance"], AppID: values["app"], DeploymentID: values["deployment"],
		NodeID: values["node"], WakeID: values["wake"], RequestReason: values["reason"], Method: values["method"], DispatchedAt: at,
	}
	if err := validate(frame); err != nil {
		return zero, err
	}
	if frame.NodeID != localNodeID {
		return zero, fmt.Errorf("checkpoint wake transport: foreign node: %w", state.ErrConflict)
	}
	return frame, nil
}

// IncomingPresent recognizes even malformed or future envelopes, allowing an
// unsupported receiver to refuse them instead of silently using a generic wake.
func IncomingPresent(ctx context.Context) bool {
	md, _ := metadata.FromIncomingContext(ctx)
	for key := range md {
		if strings.HasPrefix(key, prefix) {
			return true
		}
	}
	return false
}
