// Package financial implements deterministic cost allocation from retained
// usage evidence and historical price contracts (ADR-431). It does not infer
// invoice facts, prices, or missing usage from operational telemetry.
package financial

import (
	"errors"
	"fmt"
	"math/big"
	"sort"
	"time"
)

var (
	ErrInvalid  = errors.New("financial: invalid evidence or contract")
	ErrOverflow = errors.New("financial: amount exceeds int64")
)

// Price fixes one meter's rate and shared account allowance for a usage period.
// MillicentsPerUnit / UnitQuantity is an exact rational rate. A zero rate is a
// valid included meter; missing pricing must be represented as missing coverage.
type Price struct {
	Version           string `json:"version"`
	Meter             string `json:"meter"`
	Currency          string `json:"currency"`
	Unit              string `json:"unit"`
	UnitQuantity      int64  `json:"unit_quantity"`
	MillicentsPerUnit int64  `json:"millicents_per_unit"`
	IncludedQuantity  int64  `json:"included_quantity"`
}

type Attribution struct {
	AppID         string `json:"app_id,omitempty"`
	JobID         string `json:"job_id,omitempty"`
	ProjectID     string `json:"project_id,omitempty"`
	EnvironmentID string `json:"environment_id,omitempty"`
	DeploymentID  string `json:"deployment_id,omitempty"`
	Name          string `json:"name,omitempty"`
}

// Evidence is an immutable, attributable meter quantity. A negative quantity
// is accepted only as an explicit correction referring to its original source.
type Evidence struct {
	ID               string      `json:"id"`
	AccountID        string      `json:"account_id"`
	SourceID         string      `json:"source_id"`
	CorrectsSourceID string      `json:"corrects_source_id,omitempty"`
	Meter            string      `json:"meter"`
	Quantity         int64       `json:"quantity"`
	Start            time.Time   `json:"start"`
	End              time.Time   `json:"end"`
	ObservedAt       time.Time   `json:"observed_at"`
	Attribution      Attribution `json:"attribution"`
}

type Allocation struct {
	Attribution         Attribution `json:"attribution"`
	Quantity            int64       `json:"quantity"`
	GrossMillicents     int64       `json:"gross_millicents"`
	AllowanceMillicents int64       `json:"allowance_millicents"`
	NetMillicents       int64       `json:"net_millicents"`
}

type MeterCost struct {
	Price               Price        `json:"price"`
	Quantity            int64        `json:"quantity"`
	IncludedQuantity    int64        `json:"included_quantity"`
	GrossMillicents     int64        `json:"gross_millicents"`
	AllowanceMillicents int64        `json:"allowance_millicents"`
	NetMillicents       int64        `json:"net_millicents"`
	AllocationMethod    string       `json:"allocation_method"`
	Allocations         []Allocation `json:"allocations"`
}

// CostMeter applies the allowance once and proportionally allocates the final
// account-meter charge. Largest-remainder allocation guarantees exact sums even
// with fractional prices. Ordering by stable attribution makes replay invariant.
func CostMeter(accountID string, start, end time.Time, price Price, entries []Evidence) (MeterCost, error) {
	out := MeterCost{Price: price, AllocationMethod: "quantity_share_largest_remainder_v1", Allocations: []Allocation{}}
	if accountID == "" || !start.Before(end) || price.Version == "" || price.Meter == "" || price.Currency != "EUR" || price.Unit == "" || price.UnitQuantity <= 0 || price.MillicentsPerUnit < 0 || price.IncludedQuantity < 0 {
		return out, ErrInvalid
	}
	quantities := map[Attribution]*big.Int{}
	seen := map[string]Evidence{}
	for _, e := range entries {
		if e.AccountID != accountID || e.Meter != price.Meter || e.SourceID == "" || !e.Start.Before(e.End) || e.Start.Before(start) || e.End.After(end) || (e.Quantity < 0 && e.CorrectsSourceID == "") || e.CorrectsSourceID == e.SourceID {
			return out, fmt.Errorf("%w: source %s", ErrInvalid, e.SourceID)
		}
		if old, ok := seen[e.SourceID]; ok {
			if !sameEvidence(old, e) {
				return out, fmt.Errorf("%w: conflicting replay %s", ErrInvalid, e.SourceID)
			}
			continue
		}
		seen[e.SourceID] = e
		if quantities[e.Attribution] == nil {
			quantities[e.Attribution] = new(big.Int)
		}
		quantities[e.Attribution].Add(quantities[e.Attribution], big.NewInt(e.Quantity))
	}
	// Corrections cannot be a substitute for missing originals or move usage to
	// another workload. Partial period readers must include referenced originals.
	corrections := map[string]*big.Int{}
	for _, e := range seen {
		if e.CorrectsSourceID == "" {
			continue
		}
		original, ok := seen[e.CorrectsSourceID]
		if !ok || original.CorrectsSourceID != "" || original.Attribution != e.Attribution || original.Quantity < 0 || !original.Start.Equal(e.Start) || !original.End.Equal(e.End) {
			return out, fmt.Errorf("%w: correction lineage %s", ErrInvalid, e.SourceID)
		}
		if corrections[e.CorrectsSourceID] == nil {
			corrections[e.CorrectsSourceID] = new(big.Int).SetInt64(original.Quantity)
		}
		corrections[e.CorrectsSourceID].Add(corrections[e.CorrectsSourceID], big.NewInt(e.Quantity))
	}
	for _, q := range corrections {
		if q.Sign() < 0 {
			return out, fmt.Errorf("%w: correction exceeds original usage", ErrInvalid)
		}
	}
	keys := make([]Attribution, 0, len(quantities))
	total := new(big.Int)
	for a, q := range quantities {
		if q.Sign() < 0 {
			return out, fmt.Errorf("%w: correction exceeds workload usage", ErrInvalid)
		}
		total.Add(total, q)
		keys = append(keys, a)
	}
	if !total.IsInt64() {
		return out, ErrOverflow
	}
	out.Quantity = total.Int64()
	out.IncludedQuantity = min(out.Quantity, price.IncludedQuantity)
	gross, err := priceQuantity(total, price)
	if err != nil {
		return out, err
	}
	net, err := priceQuantity(big.NewInt(out.Quantity-out.IncludedQuantity), price)
	if err != nil {
		return out, err
	}
	out.GrossMillicents = gross
	out.NetMillicents = net
	out.AllowanceMillicents = gross - net
	sort.Slice(keys, func(i, j int) bool { return attributionKey(keys[i]) < attributionKey(keys[j]) })
	weights := make([]*big.Int, len(keys))
	for i, k := range keys {
		weights[i] = quantities[k]
	}
	// Allocate the allowance and net independently, then add them to form
	// gross. This preserves each row's gross=allowance+net as well as totals.
	nets := allocate(net, weights, total)
	allowances := allocate(gross-net, weights, total)
	for i, k := range keys {
		if !weights[i].IsInt64() {
			return out, ErrOverflow
		}
		out.Allocations = append(out.Allocations, Allocation{Attribution: k, Quantity: weights[i].Int64(), GrossMillicents: nets[i] + allowances[i], AllowanceMillicents: allowances[i], NetMillicents: nets[i]})
	}
	return out, nil
}

func priceQuantity(q *big.Int, p Price) (int64, error) {
	n := new(big.Int).Mul(q, big.NewInt(p.MillicentsPerUnit))
	n.Quo(n, big.NewInt(p.UnitQuantity))
	if !n.IsInt64() {
		return 0, ErrOverflow
	}
	return n.Int64(), nil
}

func allocate(amount int64, weights []*big.Int, total *big.Int) []int64 {
	out := make([]int64, len(weights))
	if amount == 0 || total.Sign() == 0 {
		return out
	}
	type remainder struct {
		index int
		value *big.Int
	}
	rem := make([]remainder, len(weights))
	var assigned int64
	for i, w := range weights {
		n := new(big.Int).Mul(big.NewInt(amount), w)
		q, r := new(big.Int), new(big.Int)
		q.QuoRem(n, total, r)
		out[i] = q.Int64()
		assigned += out[i]
		rem[i] = remainder{i, r}
	}
	sort.SliceStable(rem, func(i, j int) bool { return rem[i].value.Cmp(rem[j].value) > 0 })
	for i := int64(0); i < amount-assigned; i++ {
		out[rem[i].index]++
	}
	return out
}

func attributionKey(a Attribution) string {
	return a.AppID + "\x00" + a.JobID + "\x00" + a.ProjectID + "\x00" + a.EnvironmentID + "\x00" + a.DeploymentID + "\x00" + a.Name
}

func sameEvidence(a, b Evidence) bool {
	return a.AccountID == b.AccountID && a.SourceID == b.SourceID && a.CorrectsSourceID == b.CorrectsSourceID && a.Meter == b.Meter && a.Quantity == b.Quantity && a.Start.Equal(b.Start) && a.End.Equal(b.End) && a.Attribution == b.Attribution
}
