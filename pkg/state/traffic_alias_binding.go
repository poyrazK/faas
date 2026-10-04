// adr: 531
package state

import (
	"context"
	"sort"
	"strings"

	"github.com/onebox-faas/faas/pkg/hostidentity"
)

type trafficNamedHostClaim struct{ Host, App, Account string }

// A stored alias reserves its label even when its target cannot serve. Only
// removal of the mapping can publish a legacy primary slug with that label.
func trafficPrimaryOwnerView(view trafficHostAnalysis, aliases []trafficNamedHostClaim) trafficHostAnalysis {
	if len(aliases) == 0 {
		return view
	}
	reserved := make(map[string]bool, len(aliases))
	for _, claim := range aliases {
		reserved[claim.Host] = true
	}
	primary := make([]string, 0, len(view.PrimaryHosts))
	for _, host := range view.PrimaryHosts {
		if !reserved[host] {
			primary = append(primary, host)
		}
	}
	view.PrimaryHosts = primary
	return view
}

func (m *MemStore) memTrafficNamedClaimsLocked(ctx context.Context, change memTrafficPolicyChange) ([]trafficNamedHostClaim, []trafficNamedHostClaim, error) {
	var aliases, primaries []trafficNamedHostClaim
	if m.trafficAppsSuffix == "" {
		return nil, nil, ctx.Err()
	}
	err := visitMemTrafficRows(ctx, m.deploymentAliases, change.Aliases, func(alias DeploymentAlias) error {
		if alias.AppID == "" || alias.Name == "" {
			return nil
		}
		label, valid := hostidentity.DeploymentAliasLabel(alias.AppID, alias.Name)
		if !valid {
			return nil
		}
		app, proposed := change.Apps[alias.AppID]
		if !proposed {
			app = m.apps[alias.AppID]
		}
		aliases = append(aliases, trafficNamedHostClaim{Host: label + m.trafficAppsSuffix, App: alias.AppID, Account: app.AccountID})
		return checkMemTrafficAnalysisInputs(len(aliases))
	})
	if err == nil {
		err = visitMemTrafficRows(ctx, m.apps, change.Apps, func(app App) error {
			if app.ID == "" || !strings.HasPrefix(app.Slug, "tag-") {
				return nil
			}
			host := hostidentity.BuildPrimaryAppHost(m.trafficAppsSuffix, app.Slug)
			if host != "" {
				primaries = append(primaries, trafficNamedHostClaim{Host: host, App: app.ID, Account: app.AccountID})
			}
			return checkMemTrafficAnalysisInputs(len(aliases) + len(primaries))
		})
	}
	if err != nil {
		return nil, nil, err
	}
	for _, claims := range [][]trafficNamedHostClaim{aliases, primaries} {
		sort.Slice(claims, func(i, j int) bool {
			if claims[i].Host != claims[j].Host {
				return claims[i].Host < claims[j].Host
			}
			return claims[i].App < claims[j].App
		})
	}
	return aliases, primaries, ctx.Err()
}
