package main

import (
	"context"
	"crypto/tls"
	"fmt"
	"log/slog"
	"time"

	apidpb "github.com/onebox-faas/faas/api/proto/onebox/faas/apid/v1"
	"github.com/onebox-faas/faas/pkg/apidgrpc"
	"github.com/onebox-faas/faas/pkg/gateway"
	"github.com/onebox-faas/faas/pkg/usageoutbox"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// confirmConsumerUsageReceiver fences a gateway upgrade against an older apid
// that would ignore usage_outboxed on collapsed debugger rows and double count.
// The deliberately malformed probe must be rejected before any database write.
func confirmConsumerUsageReceiver(ctx context.Context, target string, tlsCfg *tls.Config) error {
	probeCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	client, err := apidgrpc.DialRequestTelemetry(probeCtx, target, tlsCfg)
	if err != nil {
		return err
	}
	defer func() { _ = client.Close() }()
	_, err = client.RecordConsumerUsage(probeCtx, &apidpb.ConsumerUsageEvent{})
	if status.Code(err) == codes.InvalidArgument {
		return nil
	}
	if err == nil {
		return fmt.Errorf("consumer usage receiver accepted invalid probe")
	}
	return fmt.Errorf("consumer usage receiver is not compatible: %w", err)
}

// deliverConsumerUsage never removes an event on timeout, DB error, lost
// acknowledgement, or an older apid without the RPC. Event IDs make replay
// safe after either process restarts.
func deliverConsumerUsage(ctx context.Context, q *usageoutbox.Outbox, target string, tlsCfg *tls.Config, log *slog.Logger, metrics *gateway.Metrics) {
	var client *apidgrpc.RequestTelemetryClientImpl
	defer func() {
		if client != nil {
			_ = client.Close()
		}
	}()
	var failures int
	for ctx.Err() == nil {
		item, ok, err := q.Next()
		if err == nil && !ok {
			select {
			case <-ctx.Done():
				return
			case <-time.After(250 * time.Millisecond):
				continue
			}
		}
		if err == nil {
			if client == nil {
				client, err = apidgrpc.DialRequestTelemetry(ctx, target, tlsCfg)
			}
			if err == nil {
				event := item.Event
				callCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
				var receipt *apidpb.ConsumerUsageReceipt
				receipt, err = client.RecordConsumerUsage(callCtx, &apidpb.ConsumerUsageEvent{
					EventId: event.EventID, AccountId: event.AccountID, AppId: event.AppID,
					ConsumerId: event.ConsumerID, PlatformTenantId: event.PlatformTenantID,
					PlatformTenantSurfaceId:              event.PlatformTenantSurfaceID,
					PlatformTenantJwtAuthorizationRuleId: event.PlatformTenantJWTAuthorizationRuleID,
					WindowStartUnixMs:                    event.WindowStart.UnixMilli(), RequestCount: event.RequestCount,
					ErrorCount: event.ErrorCount, BillableUnits: event.BillableUnits,
				})
				cancel()
				if err == nil && receipt == nil {
					err = fmt.Errorf("empty usage acknowledgement")
				}
				if err == nil && event.PlatformTenantSurfaceID != "" && !receipt.GetSurfaceAttributionSupported() {
					err = fmt.Errorf("apid does not acknowledge tenant-surface attribution")
				}
				if err == nil && event.PlatformTenantJWTAuthorizationRuleID != "" && !receipt.GetJwtTenantAttributionSupported() {
					err = fmt.Errorf("apid does not acknowledge JWT tenant attribution")
				}
				if err == nil {
					err = q.Ack(item)
				}
			}
		}
		if err == nil {
			metrics.IncUsageDelivered()
			failures = 0
			continue
		}
		if client != nil {
			_ = client.Close()
			client = nil
		}
		metrics.IncUsageDeliveryFailure()
		failures++
		if failures == 1 || failures%12 == 0 {
			log.Warn("consumer usage delivery blocked; retaining event for replay", "err", err, "pending", q.Stats().PendingRecords)
		}
		backoff := time.Duration(failures) * time.Second
		if backoff > 30*time.Second {
			backoff = 30 * time.Second
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		}
	}
}

// monitorConsumerUsage drains the gateway from rotation before the bounded
// outbox fills. Existing in-flight responses can still race a disk failure;
// failures are counted and never misreported as delivered.
func monitorConsumerUsage(ctx context.Context, q *usageoutbox.Outbox, signal *gateway.ReadySignal, metrics *gateway.Metrics) {
	update := func() {
		stats := q.Stats()
		metrics.SetUsageOutboxPending(stats.PendingRecords, stats.PendingBytes)
		if err := q.Health(); err != nil {
			signal.Set(false, "consumer usage outbox unhealthy")
			return
		}
		if stats.PendingBytes >= stats.CapacityBytes*9/10 {
			signal.Set(false, "consumer usage outbox nearing capacity")
			return
		}
		signal.Set(true, "")
	}
	update()
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			update()
		}
	}
}
