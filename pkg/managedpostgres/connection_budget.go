package managedpostgres

import (
	"context"
	"slices"
	"sort"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/connectioncapacity"
)

// ConnectionBudgetSnapshot is a credential-free catalog read. Counts are
// potential consumers, not live SQL sessions. Admission is available only for
// exact placements with a qualified allocation and durable credential ceilings.
type ConnectionBudgetSnapshot struct {
	DatabaseID, DatabaseName, DatabaseState, BackendFingerprint string
	Generation                                                  int64
	CheckedAt                                                   time.Time
	Truncated, ModelsVerified, OtherConsumersCovered            bool
	InventoryUnavailable                                        bool
	NativeReplication                                           bool
	Bindings                                                    []connectionBudgetBinding
	Capacity                                                    *ConnectionBudgetCapacityEvidence
	Admission                                                   *connectioncapacity.Snapshot
}

// ConnectionBudgetCapacityEvidence must come from a qualified, cached observer
// of this exact database/generation and tenant-usable allocation. Compute class
// and PostgreSQL max_connections alone do not establish usable capacity.
// The canonical pool model remains advisory. Qualified pipeline mirrors use
// the independent Admission ledger and enforce login ceilings instead.
type ConnectionBudgetCapacityEvidence struct {
	DatabaseID, BackendFingerprint string
	Generation, UsableConnections  int64
	CheckedAt, ExpiresAt           time.Time
}

type connectionBudgetBinding struct {
	AppID          string   `json:"app_id"`
	Scope          string   `json:"scope"`
	Access         string   `json:"access"`
	State          string   `json:"state"`
	Plan           api.Plan `json:"plan"`
	MaxConcurrency int64    `json:"max_concurrency"`
	Rotation       bool     `json:"rotation"`
	Residents      int64    `json:"residents"`
	Parked         int64    `json:"parked"`
	Auxiliary      int64    `json:"auxiliary"`
	Tasks          int64    `json:"tasks"`
	StaleNodes     int64    `json:"stale_nodes"`
	Deployments    int64    `json:"deployments"`
	Sidecars       bool     `json:"sidecars"`
	Migrating      bool     `json:"migrating"`
}

type ConnectionBudgetStore interface {
	ReadConnectionBudget(context.Context, string, string) (ConnectionBudgetSnapshot, error)
	AppConnectionBudgetDatabaseIDs(context.Context, string, string) ([]string, error)
}

func (s *BindingService) ConnectionBudget(ctx context.Context, accountID, databaseID string) (api.ManagedPostgresConnectionBudget, error) {
	if accountID == "" || databaseID == "" {
		return api.ManagedPostgresConnectionBudget{}, ErrInvalid
	}
	ctx, cancel := context.WithTimeout(ctx, api.DataAPIConnectionBudgetReadTimeout)
	defer cancel()
	if _, err := customerDatabase(ctx, s.databases, accountID, databaseID); err != nil {
		return api.ManagedPostgresConnectionBudget{}, err
	}
	store, ok := s.bindings.(ConnectionBudgetStore)
	if !ok {
		return api.ManagedPostgresConnectionBudget{}, ErrUnsupported
	}
	snapshot, err := store.ReadConnectionBudget(ctx, accountID, databaseID)
	if err != nil {
		return api.ManagedPostgresConnectionBudget{}, err
	}
	return describeConnectionBudget(snapshot, s.now()), nil
}

func (s *BindingService) AppConnectionBudgets(ctx context.Context, accountID, appID, slug string) (api.DataAPIConnectionBudgets, error) {
	out := api.DataAPIConnectionBudgets{Version: 1, App: slug, State: "not_applicable", Databases: []api.ManagedPostgresConnectionBudget{}}
	if accountID == "" || appID == "" {
		return out, ErrInvalid
	}
	ctx, cancel := context.WithTimeout(ctx, api.DataAPIConnectionBudgetReadTimeout)
	defer cancel()
	store, ok := s.bindings.(ConnectionBudgetStore)
	if !ok {
		return out, ErrUnsupported
	}
	ids, err := store.AppConnectionBudgetDatabaseIDs(ctx, accountID, appID)
	if err != nil {
		return out, err
	}
	if len(ids) > api.DataAPIConnectionBudgetMaxDatabases {
		out.Truncated = true
		ids = ids[:api.DataAPIConnectionBudgetMaxDatabases]
	}
	for _, id := range ids {
		budget, err := s.ConnectionBudget(ctx, accountID, id)
		if err != nil {
			return out, err
		}
		out.Databases = append(out.Databases, budget)
	}
	if len(out.Databases) > 0 {
		out.State = "within_budget"
	}
	for _, budget := range out.Databases {
		if budget.State == "over_budget" {
			out.State = "over_budget"
			break
		}
		if budget.State != "within_budget" {
			out.State = "unknown"
		}
	}
	if out.Truncated && out.State != "over_budget" {
		out.State = "unknown"
	}
	return out, nil
}

func describeConnectionBudget(s ConnectionBudgetSnapshot, now time.Time) api.ManagedPostgresConnectionBudget {
	if s.Admission != nil {
		return describeAdmittedBudget(s, now)
	}
	if len(s.Bindings) > api.DataAPIConnectionBudgetMaxBindings {
		s.Truncated = true
		s.Bindings = s.Bindings[:api.DataAPIConnectionBudgetMaxBindings]
	}
	out := api.ManagedPostgresConnectionBudget{Version: 1, DatabaseID: s.DatabaseID, DatabaseName: s.DatabaseName,
		State: "unknown", Model: "canonical_runtime_pools", CheckedAt: s.CheckedAt.UTC().Format(time.RFC3339Nano),
		ExpiresAt: s.CheckedAt.Add(api.DataAPIConnectionBudgetMaxAge).UTC().Format(time.RFC3339Nano),
		Capacity:  api.ConnectionBudgetCapacity{State: "unknown"}, Reasons: []string{}}
	reasons := map[string]bool{}
	add := func(reason string) { reasons[reason] = true }
	if s.CheckedAt.IsZero() || now.Before(s.CheckedAt) || !now.Before(s.CheckedAt.Add(api.DataAPIConnectionBudgetMaxAge)) {
		add("inventory_stale")
	}
	if s.DatabaseState != string(StateReady) {
		add("database_not_ready")
	}
	if s.Truncated {
		add("inventory_truncated")
	}
	if s.InventoryUnavailable {
		add("runtime_inventory_unavailable")
	}
	if !s.ModelsVerified {
		add("runtime_pool_configuration_unverified")
	}
	if s.NativeReplication {
		add("native_replication_connections_unmodeled")
	}
	if !s.OtherConsumersCovered {
		add("external_consumers_unobserved")
	}
	groups := map[string][]connectionBudgetBinding{}
	for _, b := range s.Bindings {
		groups[b.AppID+"\x00"+b.Scope+"\x00"+b.Access] = append(groups[b.AppID+"\x00"+b.Scope+"\x00"+b.Access], b)
	}
	seen := map[string]bool{}
	for _, bindings := range groups {
		accountConnectionBudgetGroup(&out, bindings, seen, add)
	}
	out.Demand.ControlHeadroom = api.DataAPIConnectionBudgetControlHeadroom
	if out.Inventory.MigrationBindings > 0 {
		out.Demand.MigrationHeadroom = api.DataAPIConnectionBudgetMigrationHeadroom
		add("migration_demand_unbounded")
	}
	if out.Inventory.OtherBindings > 0 {
		add("client_pool_sizes_unknown")
	}
	out.Demand.ModeledPeakConnections = out.Demand.DataAPIWakeEstimate + out.Demand.AuthWakeEstimate + out.Demand.ControlHeadroom + out.Demand.MigrationHeadroom
	c := s.Capacity
	if c == nil || c.DatabaseID != s.DatabaseID || c.BackendFingerprint != s.BackendFingerprint || c.Generation != s.Generation || c.UsableConnections < 1 || c.UsableConnections > api.DataAPIConnectionBudgetMaxCount ||
		c.Generation < 1 || c.CheckedAt.IsZero() || c.CheckedAt.After(s.CheckedAt) || now.Before(c.CheckedAt) || !now.Before(c.ExpiresAt) || c.ExpiresAt.Sub(c.CheckedAt) > api.DataAPIConnectionBudgetMaxAge {
		add("capacity_unverified")
	} else {
		out.Capacity.State = "verified"
		value := c.UsableConnections
		out.Capacity.UsableConnections = &value
		if c.ExpiresAt.Before(s.CheckedAt.Add(api.DataAPIConnectionBudgetMaxAge)) {
			out.ExpiresAt = c.ExpiresAt.UTC().Format(time.RFC3339Nano)
		}
	}
	for reason := range reasons {
		out.Reasons = append(out.Reasons, reason)
	}
	sort.Strings(out.Reasons)
	if len(out.Reasons) == 0 && out.Capacity.UsableConnections != nil {
		out.State = "within_budget"
		if out.Demand.ModeledPeakConnections > *out.Capacity.UsableConnections {
			out.State = "over_budget"
		}
	}
	return out
}

func describeAdmittedBudget(s ConnectionBudgetSnapshot, now time.Time) api.ManagedPostgresConnectionBudget {
	r := s.Admission
	out := api.ManagedPostgresConnectionBudget{Version: 1, DatabaseID: s.DatabaseID, DatabaseName: s.DatabaseName, State: "unknown", Enforced: true, Model: "qualified_shared_connection_limits",
		CheckedAt: r.ObservedAt.UTC().Format(time.RFC3339Nano), ExpiresAt: r.ExpiresAt.UTC().Format(time.RFC3339Nano), Reasons: []string{}, Capacity: api.ConnectionBudgetCapacity{State: "unknown"}}
	out.Inventory.Bindings = int64(len(s.Bindings))
	out.Demand.ModeledPeakConnections = r.Reserved.Connections
	if !r.Fresh(now) || r.Connections < 1 || s.DatabaseState != string(StateReady) {
		out.Reasons = append(out.Reasons, "capacity_unverified")
		return out
	}
	out.Capacity.State = "verified"
	value := r.Connections
	out.Capacity.UsableConnections = &value
	out.State = "within_budget"
	if !r.Fits(connectioncapacity.Limits{}) {
		out.State = "over_budget"
		out.Reasons = append(out.Reasons, "reserved_capacity_exceeds_allocation")
	}
	return out
}

func accountConnectionBudgetGroup(out *api.ManagedPostgresConnectionBudget, bindings []connectionBudgetBinding, seen map[string]bool, add func(string)) {
	b := bindings[0]
	out.Inventory.Bindings += int64(len(bindings))
	for _, binding := range bindings {
		if binding.Rotation {
			out.Inventory.RotationPending++
		}
		if binding.State != string(BindingStateReady) {
			add("binding_transition")
		}
	}
	for _, count := range []int64{b.MaxConcurrency, b.Residents, b.Parked, b.Auxiliary, b.Tasks, b.StaleNodes, b.Deployments} {
		if count < 0 || count > api.DataAPIConnectionBudgetMaxCount {
			add("inventory_invalid")
			return
		}
	}
	key := b.AppID + "\x00" + b.Scope
	if !seen[key] {
		out.Inventory.ResidentInstances += b.Residents
		out.Inventory.ParkedInstances += b.Parked
		out.Inventory.AuxiliaryInstances += b.Auxiliary
		out.Inventory.ActiveTasks += b.Tasks
		out.Inventory.StaleNodeInstances += b.StaleNodes
		seen[key] = true
	}
	if b.StaleNodes > 0 {
		add("node_observations_stale")
	}
	if b.Auxiliary > 0 || b.Tasks > 0 || b.Sidecars || b.Migrating {
		add("auxiliary_connections_unmodeled")
	}
	if b.Access == string(CredentialMigration) {
		out.Inventory.MigrationBindings += int64(len(bindings))
		return
	}
	if !slices.Contains([]string{string(CredentialDataAPI), string(CredentialAuth)}, b.Access) {
		out.Inventory.OtherBindings += int64(len(bindings))
		return
	}
	if len(bindings) > 1 {
		add("multiple_runtime_bindings")
	}
	concurrency := b.MaxConcurrency
	if b.Access == string(CredentialDataAPI) {
		out.Inventory.DataAPIConsumers++
		out.Demand.DataAPIResidentEstimate += b.Residents * api.DataAPIMaxPoolConnections
	} else {
		out.Inventory.AuthConsumers++
	}
	limits, known := api.LimitsFor(b.Plan)
	if !known || concurrency < 0 || b.Residents < 0 || b.Parked < 0 {
		add("inventory_invalid")
		return
	}
	if concurrency == 0 {
		concurrency = int64(limits.MaxConcurrency)
	}
	if concurrency > int64(limits.MaxConcurrency) {
		add("concurrency_policy_unverified")
	}
	concurrency = min(concurrency, int64(limits.MaxConcurrency))
	overlap := b.Deployments > 1 || b.Residents > concurrency || b.Migrating
	for _, binding := range bindings {
		overlap = overlap || binding.Rotation
	}
	if overlap {
		out.Inventory.OverlapConsumers++
		concurrency += api.DataAPIConnectionBudgetOverlapInstances
	}
	planned := max(b.Residents+b.Parked, concurrency)
	if b.Access == string(CredentialDataAPI) {
		out.Demand.DataAPIWakeEstimate += planned * api.DataAPIMaxPoolConnections
	} else {
		out.Demand.AuthWakeEstimate += planned * api.AuthMaxPoolConnections
	}
}
