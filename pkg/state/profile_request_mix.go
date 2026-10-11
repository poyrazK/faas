package state

import (
	"context"
	"fmt"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

type ProfileRequestMix struct {
	Total            int64
	Routes, Statuses []ProfileRequestMixGroup
	Truncated        bool
}

type ProfileRequestMixGroup = api.ProfileRequestMixGroup

type ProfileRequestMixReader interface {
	ProfileRequestMix(context.Context, string, string, api.ProfileQuery) (ProfileRequestMix, error)
}

func (s *PgStore) ProfileRequestMix(ctx context.Context, accountID, appID string, window api.ProfileQuery) (ProfileRequestMix, error) {
	rows, err := sqlc.New().ProfileRequestMix(ctx, s.pool, sqlc.ProfileRequestMixParams{Route: window.Route, AccountID: accountID, AppID: appID, DeploymentID: window.DeploymentID, StartAt: profileCheckTime(window.Start), EndAt: profileCheckTime(window.End), MaxRoutes: api.ProfileRequestMixMaxRoutes + 1})
	if err != nil {
		return ProfileRequestMix{}, fmt.Errorf("read profiling request mix: %w", err)
	}
	var out ProfileRequestMix
	for _, r := range rows {
		out.Total = r.Total
		group := ProfileRequestMixGroup{Label: r.Label, Method: r.Method, Requests: r.Requests}
		if r.Dimension == "status" {
			out.Statuses = append(out.Statuses, group)
			continue
		}
		if len(out.Routes) >= api.ProfileRequestMixMaxRoutes {
			out.Truncated = true
			continue
		}
		out.Routes = append(out.Routes, group)
	}
	return out, nil
}
