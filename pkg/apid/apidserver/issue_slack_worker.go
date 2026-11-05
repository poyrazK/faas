package apidserver

import (
	"context"
	"errors"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/issueslack"
	"github.com/onebox-faas/faas/pkg/state"
)

func openIssueSlackToken(blob []byte) (string, error) {
	ids := accountIdentities()
	if len(ids) == 0 {
		return "", errors.New("slack token keys unavailable")
	}
	return issueslack.OpenToken(ids, blob)
}
func (s *server) startIssueSlackDelivery(ctx context.Context) {
	store, ok := s.store.(state.IssueSlackStore)
	if !ok {
		return
	}
	worker := issueslack.Worker{Store: store, OpenToken: openIssueSlackToken, DashboardBase: s.cliAuthURLBase}
	go func() {
		ticker := time.NewTicker(api.IssueSlackTick)
		defer ticker.Stop()
		lastCleanup := time.Time{}
		for {
			batch, cancel := context.WithTimeout(ctx, api.IssueMaintenanceInterval)
			if time.Since(lastCleanup) >= api.IssueMaintenanceInterval {
				if err := store.CleanupIssueSlackThreads(batch); err != nil && ctx.Err() == nil {
					s.log.Warn("team Slack thread cleanup failed")
				}
				lastCleanup = time.Now()
			}
			if err := worker.Batch(batch); err != nil && ctx.Err() == nil {
				s.log.Warn("team Slack delivery failed")
			}
			cancel()
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	}()
}
