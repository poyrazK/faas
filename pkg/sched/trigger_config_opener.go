package sched

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
	"github.com/onebox-faas/faas/pkg/triggerconfig"
)

// triggerConfigOpenTimeout bounds one remote open; a poller that cannot be
// built is retried at the next trigger tick.
const triggerConfigOpenTimeout = 5 * time.Second

// errTriggerWithoutApp: a trigger with no app cannot be authorized by app
// ownership, so a remote opener refuses it.
var errTriggerWithoutApp = errors.New("trigger has no app to authorize its credentials by")

// openTriggerConfig returns t's config with its credentials opened: by
// reference through the trigger config opener when one is set
// (ADR-20261110210000 slice 3g), else with this node's identities.
func (l *Loop) openTriggerConfig(ctx context.Context, t sqlc.Trigger) (json.RawMessage, error) {
	if l.triggerConfigOpener == nil {
		return triggerconfig.Open(api.TriggerKind(t.Kind), t.Config, l.triggerSecretIdentities)
	}
	if !t.AppID.Valid {
		return nil, errTriggerWithoutApp
	}
	ctx, cancel := context.WithTimeout(ctx, triggerConfigOpenTimeout)
	defer cancel()
	return l.triggerConfigOpener(ctx, t.AppID.String(), t.ID.String())
}
