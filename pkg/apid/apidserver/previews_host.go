package apidserver

import (
	"context"
	"errors"
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/previews"
	"github.com/onebox-faas/faas/pkg/authz"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/platformflags"
	"github.com/onebox-faas/faas/pkg/state"
)

// previewsHost adapts *server to previews.Host (ADR 20261021080640).
type previewsHost struct{ s *server }

var _ previews.Host = previewsHost{}

func (h previewsHost) PlatformFlags() *platformflags.Snapshot {
	return h.s.platformFlags().Snapshot()
}

func (h previewsHost) Subject(r *http.Request, acct state.Account) platformflags.Subject {
	return flagSubject(r, acct)
}

func (h previewsHost) LoadOptIns(ctx context.Context, flagKey string) (platformflags.OptIns, int64, error) {
	row, err := h.s.store.GetRuntimeConfig(ctx, platformflags.OptInsConfigKey(flagKey), state.RuntimeConfigScopeGlobal, "")
	if errors.Is(err, state.ErrRuntimeConfigNotFound) {
		return platformflags.OptIns{}, 0, nil
	}
	if err != nil {
		return platformflags.OptIns{}, 0, err
	}
	optIns, err := platformflags.ParseOptIns(row.DesiredValue)
	return optIns, row.Version, err
}

// SaveOptIns writes a customer's opt-in through the same versioned hot path
// as an operator change. Once the row is durable a lost apply race is not an
// error: the newer version already live in this process includes or
// supersedes it, and the subscriber repairs a failed acknowledgement.
func (h previewsHost) SaveOptIns(ctx context.Context, flagKey string, optIns platformflags.OptIns, expected int64, actor state.Account, reason string) error {
	key := platformflags.OptInsConfigKey(flagKey)
	row, err := h.s.store.UpsertRuntimeConfig(ctx, state.RuntimeConfigUpdate{
		Key: key, Scope: state.RuntimeConfigScopeGlobal, DesiredValue: optIns.Marshal(),
		ApplyMode: state.RuntimeConfigApplyHot, ActorID: actor.ID, Reason: reason, ExpectedVersion: &expected,
	})
	if err != nil {
		return err
	}
	if err := h.s.applyHotRuntimeConfig(ctx, row); err != nil && !errors.Is(err, state.ErrRuntimeConfigConflict) && h.s.log != nil {
		h.s.log.Warn("platform_flags.opt_in_apply_deferred", "key", key, "version", row.Version, "err", err)
	}
	_ = h.s.notif.Notify(ctx, db.NotifyRuntimeConfigChanged, key)
	return nil
}

func (h previewsHost) AuthorizeOrg(r *http.Request, action authz.OrgAction) *api.Problem {
	return authz.AuthorizeOrgAction(r.Context(), action, h.s.audit)
}

func (h previewsHost) Audit(ctx context.Context, kind string, accountID string, data map[string]any) {
	if h.s.audit != nil {
		h.s.audit.Emit(ctx, kind, &accountID, data)
	}
}
