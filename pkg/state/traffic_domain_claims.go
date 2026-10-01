// adr: 375
package state

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func readTrafficDomainClaims(ctx context.Context, reader sqlc.DBTX) ([]trafficDomainClaim, error) {
	bounded, cancel := context.WithTimeout(ctx, api.TrafficPolicyAnalysisSQLTimeout)
	defer cancel()
	row, err := sqlc.New().ReadTrafficDomainClaims(bounded, reader, sqlc.ReadTrafficDomainClaimsParams{
		MaxInputs: api.TrafficPolicyMaxAnalysisInputs, MaxBytes: api.TrafficPolicyMaxAnalysisMetadataBytes,
	})
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if bounded.Err() != nil {
			limit := api.TrafficPolicyAnalysisSQLTimeout.Milliseconds()
			return nil, analysisLimit("database_time", "milliseconds", limit, limit+1)
		}
		return nil, fmt.Errorf("state: read domain binding metadata: %w", mapErr(err))
	}
	if row.Inputs > api.TrafficPolicyMaxAnalysisInputs {
		return nil, analysisLimit("inputs", "bindings", api.TrafficPolicyMaxAnalysisInputs, row.Inputs)
	}
	if row.Bytes > api.TrafficPolicyMaxAnalysisMetadataBytes {
		return nil, analysisLimit("metadata", "bytes", api.TrafficPolicyMaxAnalysisMetadataBytes, row.Bytes)
	}
	var claims []trafficDomainClaim
	if err := json.Unmarshal(row.Data, &claims); err != nil {
		return nil, fmt.Errorf("state: decode domain binding metadata: %w", err)
	}
	return claims, nil
}

func trafficDomainClaimsOverlap(a, b string) bool {
	as, aw := WildcardDomainSuffix(a)
	bs, bw := WildcardDomainSuffix(b)
	switch {
	case aw && bw:
		return !strings.Contains(as+bs, "*") && (as == bs || strings.HasSuffix(as, "."+bs) || strings.HasSuffix(bs, "."+as))
	case aw:
		return WildcardMatchesHost(a, b)
	case bw:
		return WildcardMatchesHost(b, a)
	default:
		return strings.EqualFold(a, b)
	}
}

func trafficDomainRemovalAccounts(claims []trafficDomainClaim, domain string) []string {
	owners := make(map[string]bool)
	for _, claim := range claims {
		if claim.Account != "" && trafficDomainClaimsOverlap(domain, claim.Domain) {
			owners[claim.Account] = true
		}
	}
	accounts := make([]string, 0, len(owners))
	for account := range owners {
		accounts = append(accounts, account)
	}
	sort.Strings(accounts)
	return accounts
}

func trafficDomainClaimByName(claims []trafficDomainClaim, domain string) (trafficDomainClaim, bool) {
	for _, claim := range claims {
		if strings.EqualFold(claim.Domain, domain) {
			return claim, true
		}
	}
	return trafficDomainClaim{}, false
}

func trafficDomainOwnerView(view trafficHostAnalysis, claims []trafficDomainClaim, account string) trafficHostAnalysis {
	view.Domains = nil
	view.SelectDomains, view.DomainClaims = true, claims
	for _, claim := range claims {
		if claim.Account == account && claim.Eligible {
			view.Domains = append(view.Domains, trafficHostDomain{Domain: claim.Domain, App: claim.App, Environment: claim.Environment})
		}
	}
	return view
}

func (m *MemStore) memTrafficDomainClaimsLocked(ctx context.Context, change memTrafficPolicyChange) ([]trafficDomainClaim, error) {
	var claims []trafficDomainClaim
	err := visitMemTrafficRows(ctx, m.domains, change.Domains, func(domain CustomDomain) error {
		if domain.Domain == "" {
			return nil
		}
		app, found := m.apps[domain.AppID]
		eligible := found && domain.Verified() && app.Status != AppDeleted && api.NormalizeAppVisibility(app.Visibility) != api.AppVisibilityInternal
		if domain.EnvironmentID != "" {
			valid := false
			for _, environment := range m.projectEnvironments {
				if err := ctx.Err(); err != nil {
					return err
				}
				if environment.ID == domain.EnvironmentID && environment.AccountID == app.AccountID && environment.ProjectID == app.ProjectID {
					valid = true
					break
				}
			}
			eligible = eligible && valid
		}
		claims = append(claims, trafficDomainClaim{Domain: domain.Domain, App: domain.AppID, Environment: domain.EnvironmentID, Account: app.AccountID, Eligible: eligible})
		return checkMemTrafficAnalysisInputs(len(claims))
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(claims, func(i, j int) bool { return claims[i].Domain < claims[j].Domain })
	encoded, err := json.Marshal(claims)
	if err != nil {
		return nil, fmt.Errorf("state: encode domain binding metadata: %w", err)
	}
	if size := int64(len(encoded)); size > api.TrafficPolicyMaxAnalysisMetadataBytes {
		return nil, analysisLimit("metadata", "bytes", api.TrafficPolicyMaxAnalysisMetadataBytes, size)
	}
	return claims, nil
}

func (m *MemStore) appendMemTrafficReservationsLocked(ctx context.Context, view *trafficHostAnalysis, change memTrafficPolicyChange) error {
	add := func(kind, host string) error {
		if host == "" {
			return nil
		}
		view.Reservations = append(view.Reservations, trafficHostReservation{Kind: kind, Host: host})
		return checkMemTrafficAnalysisInputs(len(view.Groups) + len(view.Assets) + len(view.Environments) + len(view.PrimaryHosts) + len(view.AliasHosts) + len(view.Domains) + len(view.Reservations))
	}
	if err := visitMemTrafficRows(ctx, m.apps, change.Apps, func(app App) error {
		if m.trafficAppsSuffix == "" {
			return nil
		}
		return add("primary", app.Slug+m.trafficAppsSuffix)
	}); err != nil {
		return err
	}
	if err := visitMemTrafficRows(ctx, m.domains, change.Domains, func(domain CustomDomain) error { return add("domain", domain.Domain) }); err != nil {
		return err
	}
	for _, hostname := range m.tenantHostnames {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := add("tenant", hostname.Hostname); err != nil {
			return err
		}
	}
	sort.Slice(view.Reservations, func(i, j int) bool {
		a, b := view.Reservations[i], view.Reservations[j]
		return a.Kind < b.Kind || a.Kind == b.Kind && a.Host < b.Host
	})
	return nil
}
