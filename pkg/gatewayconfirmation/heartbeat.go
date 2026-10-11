package gatewayconfirmation

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

type HeartbeatStore interface {
	HeartbeatRuntimeUpgradeGateway(context.Context, string, string) error
}

func ValidateIdentity(slot, session string) error {
	for _, id := range []string{slot, session} {
		parsed, err := uuid.Parse(id)
		if err != nil || parsed == uuid.Nil || parsed.String() != id {
			return errors.New("runtime gateway identity requires canonical nonzero UUIDs")
		}
	}
	return nil
}

// RunHeartbeat reports only process liveness. It is independent of repair,
// which publishes installed weights. Neither fact substitutes for the other.
func RunHeartbeat(ctx context.Context, store HeartbeatStore, slot, session string, log *slog.Logger) {
	if ValidateIdentity(slot, session) != nil {
		log.Warn("runtime gateway heartbeat identity invalid")
		return
	}
	ticker := time.NewTicker(api.RuntimeUpgradeGatewayRepairInterval)
	defer ticker.Stop()
	for ctx.Err() == nil {
		poll, cancel := context.WithTimeout(ctx, api.RuntimeUpgradeGatewayRepairTimeout)
		err := store.HeartbeatRuntimeUpgradeGateway(poll, slot, session)
		cancel()
		if err != nil && ctx.Err() == nil {
			log.Warn("runtime gateway heartbeat pending review or retry")
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
