package outbound

import (
	"context"
	"errors"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/rehearsalfault"
)

func waitPostgresFault(ctx context.Context, store any, req api.ScenarioSimulationRequest, id, attempt string) error {
	s, ok := store.(interface {
		ScenarioPostgresFault(context.Context, string) (rehearsalfault.Row, error)
	})
	if !ok {
		return errors.New("PostgreSQL fault observation unavailable")
	}
	call, cancel := context.WithTimeout(ctx, 35*time.Second)
	defer cancel()
	ticker := time.NewTicker(25 * time.Millisecond)
	defer ticker.Stop()
	for {
		if err := call.Err(); err != nil {
			return err
		}
		r, err := s.ScenarioPostgresFault(call, id)
		if err != nil || r.AccountID != req.AccountID || r.RunID != req.RunID || r.AppID != req.AppID || r.InstanceID != req.InstanceID || r.CheckpointAttemptID != attempt || r.Error != "" {
			return errors.New("PostgreSQL fault identity or owner was lost")
		}
		if r.Activated && (r.State == "active" || r.State == "recovering" || r.State == "recovered") {
			return nil
		}
		select {
		case <-call.Done():
			return call.Err()
		case <-ticker.C:
		}
	}
}
