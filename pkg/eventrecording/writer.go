// Package eventrecording observes acknowledgment writer capabilities and liveness.
package eventrecording

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// Start registers a fresh process session. Missing deployment inventory never verifies coverage.
func Start(ctx context.Context, store state.EventAcknowledgmentWriterStore, role, slot, inventory string, log *slog.Logger) (func(), error) {
	if len(inventory) > api.EventAcknowledgmentWriterRosterMaxBytes {
		return nil, fmt.Errorf("event recording inventory exceeds size limit")
	}
	var members []state.EventAcknowledgmentWriterMember
	if strings.TrimSpace(inventory) != "" {
		dec := json.NewDecoder(strings.NewReader(inventory))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&members); err != nil {
			return nil, fmt.Errorf("event recording inventory: %w", err)
		}
		if members == nil {
			return nil, fmt.Errorf("event recording inventory must be an array")
		}
		var extra any
		if err := dec.Decode(&extra); !errors.Is(err, io.EOF) {
			return nil, fmt.Errorf("event recording inventory must contain one JSON array")
		}
		if slot == "" {
			return nil, fmt.Errorf("event recording inventory requires an explicit writer slot")
		}
	}
	if slot == "" {
		host, err := os.Hostname()
		if err != nil {
			return nil, fmt.Errorf("event recording hostname: %w", err)
		}
		slot = role + ":" + host
	}
	r := state.EventAcknowledgmentWriterRegistration{SessionID: uuid.NewString(), Role: role, Slot: slot, CapabilityVersion: api.EventAcknowledgmentWriterCapabilityVersion, Inventory: members}
	if err := state.ValidateEventAcknowledgmentWriterRegistration(r); err != nil {
		return nil, fmt.Errorf("event recording configuration: %w", err)
	}
	runCtx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	beat := func() {
		beatCtx, stop := context.WithTimeout(runCtx, api.EventSubscriptionControlTimeout)
		defer stop()
		if err := store.HeartbeatEventAcknowledgmentWriter(beatCtx, r); err != nil && runCtx.Err() == nil {
			log.Warn("event recording writer heartbeat failed", "role", role, "err", err)
		}
	}
	beat()
	go func() {
		defer close(done)
		ticker := time.NewTicker(api.EventAcknowledgmentWriterHeartbeat)
		defer ticker.Stop()
		for {
			select {
			case <-runCtx.Done():
				return
			case <-ticker.C:
				beat()
			}
		}
	}()
	return func() { cancel(); <-done }, nil
}
