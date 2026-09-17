package sched

import (
	"context"
	"errors"
	"log/slog"
	"net/netip"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/state"
)

// PrivateNetworkRouter is the additive vmmd capability used by the
// provider-neutral private-network reconciler. It intentionally stays outside
// RoutedVMM so existing scheduler fakes and alternate routers remain source
// compatible while the product surface rolls out.
type PrivateNetworkRouter interface {
	UpdatePrivateNetwork(context.Context, string, string, []netip.Prefix) error
}

// PrivateNetworkRouteApplier fans one app-level CIDR set out to the vmmds that
// own its live instances. vmmd itself fans that update out to all of its local
// instances, so schedd only sends one update per node.
type PrivateNetworkRouteApplier struct {
	store  state.Store
	router PrivateNetworkRouter
	log    *slog.Logger
}

func NewPrivateNetworkRouteApplier(store state.Store, router PrivateNetworkRouter, log *slog.Logger) *PrivateNetworkRouteApplier {
	if log == nil {
		log = slog.Default()
	}
	return &PrivateNetworkRouteApplier{store: store, router: router, log: log}
}

func (a *PrivateNetworkRouteApplier) Apply(ctx context.Context, appID string, cidrs []netip.Prefix) error {
	rows, err := a.store.ListInstancesForApp(ctx, appID)
	if err != nil {
		return err
	}
	seen := make(map[string]struct{}, len(rows))
	var errs []error
	for _, ins := range rows {
		if !state.IsLive(ins.State) || ins.NodeID == "" {
			continue
		}
		if _, ok := seen[ins.NodeID]; ok {
			continue
		}
		seen[ins.NodeID] = struct{}{}
		if err := a.router.UpdatePrivateNetwork(ctx, ins.NodeID, appID, cidrs); err != nil {
			errs = append(errs, err)
			a.log.Warn("schedd: private network vmmd update failed", "app", appID, "node", ins.NodeID, "err", err)
		}
	}
	return errors.Join(errs...)
}

// PrivateNetworkAttachmentSubscriber handles the one event the poll-based
// reconciler cannot discover after the row is deleted: detach. It immediately
// clears live rules; attach/readiness transitions remain owned by the durable
// reconciler sweep.
type PrivateNetworkAttachmentSubscriber struct {
	applier *PrivateNetworkRouteApplier
	log     *slog.Logger
	pool    *pgxpool.Pool
}

func NewPrivateNetworkAttachmentSubscriber(applier *PrivateNetworkRouteApplier, log *slog.Logger) *PrivateNetworkAttachmentSubscriber {
	if log == nil {
		log = slog.Default()
	}
	return &PrivateNetworkAttachmentSubscriber{applier: applier, log: log}
}

// WithNotificationPool enables acknowledgement of a durable LISTEN fast-path
// delivery. Replay workers acknowledge rows themselves; this hook lets the
// normal notification path close the same outbox row after cleanup succeeds.
func (s *PrivateNetworkAttachmentSubscriber) WithNotificationPool(pool *pgxpool.Pool) *PrivateNetworkAttachmentSubscriber {
	s.pool = pool
	return s
}

// Handle applies one detach event. It is shared by the live LISTEN path and
// the durable replay worker so a route-cleanup failure is retryable instead of
// being logged and acknowledged as if cleanup succeeded.
func (s *PrivateNetworkAttachmentSubscriber) Handle(ctx context.Context, n db.Notification) error {
	if n.Channel != db.NotifyPrivateNetworkAttachmentChanged {
		return nil
	}
	payload, err := db.ParseAppChangedPayload(n.Payload)
	if err != nil {
		return err
	}
	if payload.Kind != "private_network_attachment" || payload.Status != "detached" {
		return nil
	}
	if err := s.applier.Apply(ctx, payload.AppID, nil); err != nil {
		return err
	}
	return nil
}

func (s *PrivateNetworkAttachmentSubscriber) Run(ctx context.Context, ch <-chan db.Notification) error {
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case n, ok := <-ch:
			if !ok {
				return nil
			}
			if err := s.Handle(ctx, n); err != nil {
				s.log.Warn("schedd: private network detach cleanup failed", "payload", n.Payload, "err", err)
				continue
			}
			if err := db.AcknowledgeNotification(ctx, s.pool, n); err != nil && ctx.Err() == nil {
				s.log.Warn("schedd: acknowledge private network detach", "id", n.OutboxID, "err", err)
			}
		}
	}
}
