package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
)

// DataAPIConnectionBudgets contains per-database planning or enforced
// allocation snapshots. Reading this endpoint never reserves connections.
type DataAPIConnectionBudgets struct {
	Version   int                               `json:"version"`
	App       string                            `json:"app"`
	State     string                            `json:"state"`
	Truncated bool                              `json:"truncated"`
	Databases []ManagedPostgresConnectionBudget `json:"databases"`
}

type ManagedPostgresConnectionBudget struct {
	Version      int                       `json:"version"`
	DatabaseID   string                    `json:"database_id"`
	DatabaseName string                    `json:"database_name"`
	State        string                    `json:"state"`
	CheckedAt    string                    `json:"checked_at"`
	ExpiresAt    string                    `json:"expires_at"`
	Enforced     bool                      `json:"enforced"`
	Model        string                    `json:"model"`
	Inventory    ConnectionBudgetInventory `json:"inventory"`
	Demand       ConnectionBudgetDemand    `json:"demand"`
	Capacity     ConnectionBudgetCapacity  `json:"capacity"`
	Reasons      []string                  `json:"reasons"`
}

type ConnectionBudgetInventory struct {
	Bindings           int64 `json:"bindings"`
	DataAPIConsumers   int64 `json:"data_api_consumers"`
	AuthConsumers      int64 `json:"auth_consumers"`
	MigrationBindings  int64 `json:"migration_bindings"`
	OtherBindings      int64 `json:"other_bindings"`
	ResidentInstances  int64 `json:"resident_instances"`
	ParkedInstances    int64 `json:"parked_instances"`
	AuxiliaryInstances int64 `json:"auxiliary_instances"`
	ActiveTasks        int64 `json:"active_tasks"`
	RotationPending    int64 `json:"rotation_pending"`
	OverlapConsumers   int64 `json:"overlap_consumers"`
	StaleNodeInstances int64 `json:"stale_node_instances"`
}

type ConnectionBudgetDemand struct {
	DataAPIResidentEstimate int64                                `json:"data_api_resident_estimate"`
	DataAPIWakeEstimate     int64                                `json:"data_api_wake_estimate"`
	AuthWakeEstimate        int64                                `json:"auth_wake_estimate"`
	ControlHeadroom         int64                                `json:"control_headroom"`
	MigrationHeadroom       int64                                `json:"migration_headroom"`
	ModeledPeakConnections  int64                                `json:"modeled_peak_connections"`
	RuntimeObservations     *ConnectionBudgetRuntimeObservations `json:"runtime_observations,omitempty"`
}

// ConnectionBudgetRuntimeObservations covers configured resident Data API
// pools only. Future wakes, parked runtimes and other consumers remain estimates.
type ConnectionBudgetRuntimeObservations struct {
	State                 string `json:"state"`
	Source                string `json:"source"`
	ObservedInstances     int64  `json:"observed_instances"`
	UnobservedInstances   int64  `json:"unobserved_instances"`
	ParkedInstances       int64  `json:"parked_instances"`
	ConfiguredConnections int64  `json:"configured_connections"`
	CheckedAt             string `json:"checked_at,omitempty"`
	ExpiresAt             string `json:"expires_at,omitempty"`
}

type ConnectionBudgetCapacity struct {
	State             string `json:"state"`
	UsableConnections *int64 `json:"usable_connections,omitempty"`
	Source            string `json:"source,omitempty"`
	CheckedAt         string `json:"checked_at,omitempty"`
	ExpiresAt         string `json:"expires_at,omitempty"`
}

func (c *Client) GetAppDataAPIConnectionBudgets(ctx context.Context, slug string) (DataAPIConnectionBudgets, error) {
	var out DataAPIConnectionBudgets
	var raw json.RawMessage
	if err := c.do(ctx, http.MethodGet, "/v1/apps/"+url.PathEscape(slug)+"/data-api/connection-budget", nil, &raw); err != nil {
		return out, err
	}
	fields, err := connectionBudgetFields(raw, "version", "app", "state", "truncated", "databases")
	if err != nil {
		return out, err
	}
	var budgets []json.RawMessage
	if err = json.Unmarshal(fields["databases"], &budgets); err != nil {
		return out, err
	}
	for _, budget := range budgets {
		if err = validateConnectionBudgetFields(budget); err != nil {
			return out, err
		}
	}
	err = json.Unmarshal(raw, &out)
	return out, err
}

func (c *Client) GetManagedPostgresConnectionBudget(ctx context.Context, id string) (ManagedPostgresConnectionBudget, error) {
	var out ManagedPostgresConnectionBudget
	var raw json.RawMessage
	if err := c.do(ctx, http.MethodGet, "/v1/postgres/databases/"+url.PathEscape(id)+"/connection-budget", nil, &raw); err != nil {
		return out, err
	}
	if err := validateConnectionBudgetFields(raw); err != nil {
		return out, err
	}
	err := json.Unmarshal(raw, &out)
	return out, err
}

func connectionBudgetFields(raw json.RawMessage, required ...string) (map[string]json.RawMessage, error) {
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil {
		return nil, errors.New("invalid connection budget response")
	}
	for _, name := range required {
		value, ok := fields[name]
		if !ok || string(value) == "null" {
			return nil, errors.New("incomplete connection budget response")
		}
	}
	return fields, nil
}

func validateConnectionBudgetFields(raw json.RawMessage) error {
	fields, err := connectionBudgetFields(raw, "version", "database_id", "database_name", "state", "checked_at", "expires_at", "enforced", "model", "inventory", "demand", "capacity", "reasons")
	if err != nil {
		return err
	}
	if _, err = connectionBudgetFields(fields["inventory"], "bindings", "data_api_consumers", "auth_consumers", "migration_bindings", "other_bindings", "resident_instances", "parked_instances", "auxiliary_instances", "active_tasks", "rotation_pending", "overlap_consumers", "stale_node_instances"); err != nil {
		return err
	}
	demand, err := connectionBudgetFields(fields["demand"], "data_api_resident_estimate", "data_api_wake_estimate", "auth_wake_estimate", "control_headroom", "migration_headroom", "modeled_peak_connections")
	if err != nil {
		return err
	}
	if observations, ok := demand["runtime_observations"]; ok {
		if _, err = connectionBudgetFields(observations, "state", "source", "observed_instances", "unobserved_instances", "parked_instances", "configured_connections"); err != nil {
			return err
		}
	}
	_, err = connectionBudgetFields(fields["capacity"], "state")
	return err
}
