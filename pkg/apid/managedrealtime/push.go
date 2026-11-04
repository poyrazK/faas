package managedrealtime

import (
	"context"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/realtimepush"
	"github.com/onebox-faas/faas/pkg/state"
)

// Push delivery runs up to managedRealtimePushWorkers sends at once and
// claims again as soon as a worker is free, so the claim rate follows the
// backlog instead of one small batch per tick (ADR-1323, amendment of
// 2026-11-03).
const (
	managedRealtimePushWorkers    = 32
	managedRealtimePushIdle       = time.Second
	managedRealtimePushJobTimeout = 40 * time.Second
	managedRealtimePushPruneEvery = time.Minute
	managedRealtimePushPruneBatch = 1000
)

// managedRealtimePushPruner deletes old push rows in batches.
type managedRealtimePushPruner interface {
	PruneManagedRealtimePush(context.Context, int) (int64, error)
}

func (s *Service) RunManagedRealtimePush(ctx context.Context) {
	if !s.HistoryPreviewEnabled {
		return
	}
	store, ok := s.Host.Store().(state.ManagedRealtimePushStore)
	if !ok {
		return
	}
	client := realtimepush.NewClient()
	defer client.CloseIdleConnections()
	tokens := &realtimepush.TokenCache{}
	log := s.Host.Logger()
	if log == nil {
		log = slog.Default()
	}
	free := make(chan struct{}, managedRealtimePushWorkers)
	var wg sync.WaitGroup
	defer wg.Wait()
	inflight := 0
	var pruned time.Time
	for {
		// Collect finished workers without waiting.
	collect:
		for {
			select {
			case <-free:
				inflight--
			default:
				break collect
			}
		}
		if pruner, ok := store.(managedRealtimePushPruner); ok && time.Since(pruned) >= managedRealtimePushPruneEvery {
			pruned = time.Now()
			pruneCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
			if _, err := pruner.PruneManagedRealtimePush(pruneCtx, managedRealtimePushPruneBatch); err != nil && ctx.Err() == nil {
				log.Warn("realtime push prune failed", "err", err)
			}
			cancel()
		}
		claimed := 0
		if room := managedRealtimePushWorkers - inflight; room > 0 {
			claimCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
			jobs, err := store.ClaimManagedRealtimePush(claimCtx, room)
			cancel()
			if err != nil && ctx.Err() == nil {
				log.Warn("realtime push claim failed", "err", err)
			}
			for _, job := range jobs {
				inflight++
				wg.Add(1)
				go func(j state.ManagedRealtimePushDelivery) {
					defer wg.Done()
					defer func() { free <- struct{}{} }()
					pass, cancel := context.WithTimeout(ctx, managedRealtimePushJobTimeout)
					defer cancel()
					s.deliverManagedRealtimePush(pass, store, client, tokens, log, j)
				}(job)
			}
			claimed = len(jobs)
			if claimed == room {
				continue // more may be due
			}
		}
		idle := time.NewTimer(managedRealtimePushIdle)
		select {
		case <-ctx.Done():
			idle.Stop()
			return
		case <-free:
			inflight--
		case <-idle.C:
		}
		idle.Stop()
	}
}

// deliverManagedRealtimePush prepares, sends and completes one claimed
// delivery.
func (s *Service) deliverManagedRealtimePush(pass context.Context, store state.ManagedRealtimePushStore, client *http.Client, tokens *realtimepush.TokenCache, log *slog.Logger, j state.ManagedRealtimePushDelivery) {
	preferences, ok := s.Host.Store().(state.ManagedRealtimePushPreferencesStore)
	if !ok {
		return
	}
	if j.DigestID == "" {
		ready, e := preferences.PrepareManagedRealtimePush(pass, j.ID, j.Lease)
		if e != nil || !ready {
			return
		}
	}
	digests, ok := s.Host.Store().(state.ManagedRealtimePushDigestStore)
	if !ok {
		return
	}
	batch, e := digests.AcquireManagedRealtimePushDigest(pass, j.ID, j.Lease)
	if e != nil || batch == nil {
		if e != nil && pass.Err() == nil {
			log.Warn("realtime push digest preparation failed", "delivery_id", j.ID, "err", e)
		}
		return
	}
	batch, e = digests.PrepareManagedRealtimePushDigest(pass, batch.DigestID, batch.Lease)
	if e != nil || batch == nil {
		if e != nil && pass.Err() == nil {
			log.Warn("realtime push digest preparation failed", "delivery_id", j.ID, "err", e)
		}
		return
	}
	var cfg realtimepush.Config
	var target realtimepush.Target
	result := realtimepush.Result{Retry: true, Code: "credential_unavailable"}
	label := api.RealtimePushDeviceNamespacePrefix + batch.EndpointID + ":" + batch.Principal + ":" + batch.Device
	if s.openPush(pass, batch.Config, pushProviderLabel(batch.EndpointID, batch.Provider), &cfg) == nil && s.openPush(pass, batch.Target, label, &target) == nil && cfg.Provider == batch.Provider {
		batch, e = digests.PrepareManagedRealtimePushDigest(pass, batch.DigestID, batch.Lease)
		if e != nil || batch == nil {
			if e != nil && pass.Err() == nil {
				log.Warn("realtime push digest preparation failed", "delivery_id", j.ID, "err", e)
			}
			return
		}
		active, e := store.ManagedRealtimePushLeaseActive(pass, batch.ID, batch.Lease)
		if e != nil || !active {
			return
		}
		if batch.DigestCount > 1 {
			cfg.Body = state.ManagedRealtimePushDigestBody(*batch)
		}
		sendCtx, stop := context.WithTimeout(pass, 20*time.Second)
		result = tokens.Send(sendCtx, client, cfg, target, realtimepush.Notification{Priority: batch.Priority, Category: batch.Category, DeliveryID: batch.DigestID, EndpointID: batch.EndpointID, MessageID: batch.MessageID, Sequence: batch.Sequence, GroupKey: batch.GroupKey, MessageCount: batch.DigestCount})
		stop()
	}
	if err := digests.CompleteManagedRealtimePushDigest(pass, batch.DigestID, batch.Lease, result.StatusCode, result.Code, result.Retry, result.InvalidTarget); err != nil && pass.Err() == nil {
		log.Warn("realtime push digest completion failed", "digest_id", batch.DigestID, "err", err)
	}
}
