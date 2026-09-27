package state

import (
	"context"
	"encoding/json"
	"fmt"
	"net/netip"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// OutboundFlowEvent contains only the original connection tuple and the
// owner copied from vmmd's live lease. No packets, DNS names, or HTTP data
// are stored. An absent app/deployment is valid for disposable executions.
type OutboundFlowEvent struct {
	ID              string    `json:"id"`
	ObservedAt      time.Time `json:"observed_at"`
	NodeID          string    `json:"node_id"`
	InstanceID      string    `json:"instance_id"`
	AccountID       string    `json:"account_id"`
	AppID           string    `json:"app_id,omitempty"`
	DeploymentID    string    `json:"deployment_id,omitempty"`
	SourceIP        string    `json:"source_ip"`
	SourcePort      uint16    `json:"source_port"`
	DestinationIP   string    `json:"destination_ip"`
	DestinationPort uint16    `json:"destination_port"`
	// ReplyDestination is the return address/port in the host conntrack
	// namespace. An upstream provider can translate it again.
	ReplyDestinationIP   string  `json:"reply_destination_ip,omitempty"`
	ReplyDestinationPort *uint16 `json:"reply_destination_port,omitempty"`
	Protocol             string  `json:"protocol"`
}

func (e OutboundFlowEvent) validate() error {
	for _, id := range []string{e.ID, e.NodeID, e.InstanceID, e.AccountID} {
		if _, err := uuid.Parse(id); err != nil {
			return fmt.Errorf("outbound flow: invalid required id: %w", err)
		}
	}
	for _, id := range []string{e.AppID, e.DeploymentID} {
		if id != "" {
			if _, err := uuid.Parse(id); err != nil {
				return fmt.Errorf("outbound flow: invalid optional id: %w", err)
			}
		}
	}
	if e.ObservedAt.IsZero() || (e.Protocol != "tcp" && e.Protocol != "udp") {
		return fmt.Errorf("outbound flow: missing timestamp or unsupported protocol")
	}
	if _, err := netip.ParseAddr(e.SourceIP); err != nil {
		return fmt.Errorf("outbound flow: invalid source IP: %w", err)
	}
	if _, err := netip.ParseAddr(e.DestinationIP); err != nil {
		return fmt.Errorf("outbound flow: invalid destination IP: %w", err)
	}
	if (e.ReplyDestinationIP == "") != (e.ReplyDestinationPort == nil) {
		return fmt.Errorf("outbound flow: incomplete reply destination")
	}
	if e.ReplyDestinationIP != "" {
		if _, err := netip.ParseAddr(e.ReplyDestinationIP); err != nil {
			return fmt.Errorf("outbound flow: invalid reply destination IP: %w", err)
		}
	}
	return nil
}

// InsertOutboundFlowEvents batches one short host observation interval into
// one atomic statement. Stable event IDs make retries safe after an uncertain
// database timeout; account foreign keys still reject erased accounts.
func (s *PgStore) InsertOutboundFlowEvents(ctx context.Context, events []OutboundFlowEvent) error {
	if len(events) == 0 {
		return nil
	}
	if s == nil || s.pool == nil {
		return fmt.Errorf("outbound flow: nil database pool")
	}
	for _, event := range events {
		if err := event.validate(); err != nil {
			return err
		}
	}
	payload, err := json.Marshal(events)
	if err != nil {
		return fmt.Errorf("outbound flow: encode batch: %w", err)
	}
	_, err = sqlc.New().InsertOutboundFlowEvents(ctx, s.pool, payload)
	if err != nil {
		return fmt.Errorf("outbound flow: insert batch: %w", err)
	}
	return nil
}

// DeleteOutboundFlowEventsBefore removes at most limit old rows per call.
func (s *PgStore) DeleteOutboundFlowEventsBefore(ctx context.Context, cutoff time.Time, limit int) (int64, error) {
	if s == nil || s.pool == nil {
		return 0, fmt.Errorf("outbound flow: nil database pool")
	}
	if limit <= 0 || limit > 10000 {
		return 0, fmt.Errorf("outbound flow: invalid retention batch size %d", limit)
	}
	return sqlc.New().DeleteOutboundFlowEventsBefore(ctx, s.pool, sqlc.DeleteOutboundFlowEventsBeforeParams{
		Cutoff: pgtype.Timestamptz{Time: cutoff, Valid: true}, BatchSize: int32(limit),
	})
}
