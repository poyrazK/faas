package financial

import (
	"fmt"
	"math/big"
	"sort"
	"time"
)

type VersionedEvidence struct {
	Evidence     Evidence
	PriceVersion string
}

type ContractCosts struct {
	Meter            string      `json:"meter"`
	Quantity         int64       `json:"quantity"`
	IncludedQuantity int64       `json:"included_quantity"`
	NetMillicents    int64       `json:"net_millicents"`
	AllowanceMethod  string      `json:"allowance_method"`
	Contracts        []MeterCost `json:"contracts"`
}

// CostContracts shares one account allowance across all historical versions of
// a meter. The largest recorded allowance is the period grant; its quantities
// are allocated proportionally to usage across versions, then each version is
// priced exactly. A plan downgrade cannot revoke an already granted allowance.
// Provider reconciliation remains explicit during the dual-run migration.
func CostContracts(account string, start, end time.Time, prices []Price, entries []VersionedEvidence) (ContractCosts, error) {
	out := ContractCosts{AllowanceMethod: "maximum_period_grant_quantity_share_v1", Contracts: []MeterCost{}}
	if len(prices) == 0 {
		return out, ErrInvalid
	}
	byVersion := map[string]Price{}
	for _, p := range prices {
		if _, err := CostMeter(account, start, end, p, nil); err != nil {
			return out, err
		}
		if old, ok := byVersion[p.Version]; ok && old != p {
			return out, ErrInvalid
		}
		if out.Meter == "" {
			out.Meter = p.Meter
		}
		if p.Meter != out.Meter || p.Unit != prices[0].Unit {
			return out, ErrInvalid
		}
		byVersion[p.Version] = p
		out.IncludedQuantity = max(out.IncludedQuantity, p.IncludedQuantity)
	}
	groups := map[string][]Evidence{}
	weights := map[string]*big.Int{}
	seen := map[string]VersionedEvidence{}
	total := new(big.Int)
	for _, row := range entries {
		if _, ok := byVersion[row.PriceVersion]; !ok {
			return out, fmt.Errorf("%w: missing price version", ErrInvalid)
		}
		if old, ok := seen[row.Evidence.SourceID]; ok {
			if old.PriceVersion != row.PriceVersion || !sameEvidence(old.Evidence, row.Evidence) {
				return out, ErrInvalid
			}
			continue
		}
		seen[row.Evidence.SourceID] = row
		groups[row.PriceVersion] = append(groups[row.PriceVersion], row.Evidence)
		if weights[row.PriceVersion] == nil {
			weights[row.PriceVersion] = new(big.Int)
		}
		weights[row.PriceVersion].Add(weights[row.PriceVersion], big.NewInt(row.Evidence.Quantity))
		total.Add(total, big.NewInt(row.Evidence.Quantity))
	}
	if total.Sign() < 0 {
		return out, ErrInvalid
	}
	if !total.IsInt64() {
		return out, ErrOverflow
	}
	out.Quantity = total.Int64()
	out.IncludedQuantity = min(out.IncludedQuantity, out.Quantity)
	versions := make([]string, 0, len(byVersion))
	for version := range byVersion {
		versions = append(versions, version)
	}
	sort.Strings(versions)
	quantities := make([]*big.Int, len(versions))
	for i, version := range versions {
		q := weights[version]
		if q == nil {
			q = new(big.Int)
		}
		if q.Sign() < 0 {
			return out, ErrInvalid
		}
		quantities[i] = q
	}
	allowances := allocate(out.IncludedQuantity, quantities, total)
	net := new(big.Int)
	for i, version := range versions {
		p := byVersion[version]
		original := p
		p.IncludedQuantity = allowances[i]
		cost, err := CostMeter(account, start, end, p, groups[version])
		if err != nil {
			return out, err
		}
		cost.Price = original
		out.Contracts = append(out.Contracts, cost)
		net.Add(net, big.NewInt(cost.NetMillicents))
	}
	if !net.IsInt64() {
		return out, ErrOverflow
	}
	out.NetMillicents = net.Int64()
	return out, nil
}
