package sched

import (
	"context"
	"errors"
	"log/slog"
	"net/netip"

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
}

func NewPrivateNetworkAttachmentSubscriber(applier *PrivateNetworkRouteApplier, log *slog.Logger) *PrivateNetworkAttachmentSubscriber {
	if log == nil {
		log = slog.Default()
	}
	return &PrivateNetworkAttachmentSubscriber{applier: applier, log: log}
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
			if n.Channel != db.NotifyAppChanged {
				continue
			}
			payload, err := db.ParseAppChangedPayload(n.Payload)
			if err != nil || payload.Kind != "private_network_attachment" || payload.Status != "detached" || payload.AppID == "" {
				continue
			}
			if err := s.applier.Apply(ctx, payload.AppID, nil); err != nil {
				s.log.Warn("schedd: private network detach cleanup failed", "app", payload.AppID, "err", err)
			}
		}
	}
}
