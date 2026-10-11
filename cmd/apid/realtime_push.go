package main

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/onebox-faas/faas/pkg/realtimepush"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *server) runManagedRealtimePush(ctx context.Context) {
	if !s.realtimeHistoryPreviewEnabled {
		return
	}
	store, ok := s.store.(state.ManagedRealtimePushStore)
	if !ok {
		return
	}
	client := realtimepush.NewClient()
	defer client.CloseIdleConnections()
	log := s.log
	if log == nil {
		log = slog.Default()
	}
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	run := func() {
		pass, cancel := context.WithTimeout(ctx, 40*time.Second)
		defer cancel()
		jobs, err := store.ClaimManagedRealtimePush(pass, 8)
		if err != nil {
			if pass.Err() == nil {
				log.Warn("realtime push claim failed", "err", err)
			}
			return
		}
		var wg sync.WaitGroup
		for _, job := range jobs {
			wg.Add(1)
			go func(j state.ManagedRealtimePushDelivery) {
				defer wg.Done()
				preferences, ok := s.store.(state.ManagedRealtimePushPreferencesStore)
				if !ok {
					return
				}
				if j.DigestID == "" {
					ready, e := preferences.PrepareManagedRealtimePush(pass, j.ID, j.Lease)
					if e != nil || !ready {
						return
					}
				}
				digests, ok := s.store.(state.ManagedRealtimePushDigestStore)
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
				label := "realtime-push-device:" + batch.EndpointID + ":" + batch.Principal + ":" + batch.Device
				if openPush(pass, batch.Config, pushProviderLabel(batch.EndpointID, batch.Provider), &cfg) == nil && openPush(pass, batch.Target, label, &target) == nil && cfg.Provider == batch.Provider {
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
					result = realtimepush.Send(sendCtx, client, cfg, target, realtimepush.Notification{Priority: batch.Priority, Category: batch.Category, DeliveryID: batch.DigestID, EndpointID: batch.EndpointID, MessageID: batch.MessageID, Sequence: batch.Sequence, GroupKey: batch.GroupKey, MessageCount: batch.DigestCount})
					stop()
				}
				if err := digests.CompleteManagedRealtimePushDigest(pass, batch.DigestID, batch.Lease, result.StatusCode, result.Code, result.Retry, result.InvalidTarget); err != nil && pass.Err() == nil {
					log.Warn("realtime push digest completion failed", "digest_id", batch.DigestID, "err", err)
				}

			}(job)
		}
		wg.Wait()
	}
	run()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			run()
		}
	}
}
