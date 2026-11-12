package api

import (
	"fmt"
	"strconv"
	"strings"
)

// PostgresBindingProbeCheck is one independently observable stage in a
// managed PostgreSQL binding canary.
type PostgresBindingProbeCheck struct {
	Status string `json:"status"`
	Detail string `json:"detail,omitempty"`
}

// PostgresBindingProbeReport contains bounded, non-secret diagnostics from a
// read-only canary executed in the app's deployment-attached task guest. It
// deliberately excludes the database URL, host, username, and query result.
type PostgresBindingProbeReport struct {
	App            string                    `json:"app,omitempty"`
	EnvironmentKey string                    `json:"environment_key,omitempty"`
	TaskID         string                    `json:"task_id,omitempty"`
	DeploymentID   string                    `json:"deployment_id,omitempty"`
	Environment    PostgresBindingProbeCheck `json:"environment"`
	Configuration  PostgresBindingProbeCheck `json:"configuration"`
	Connection     PostgresBindingProbeCheck `json:"connection"`
	Query          PostgresBindingProbeCheck `json:"query"`
	Error          string                    `json:"error,omitempty"`
	// PoolMax is the GREGALE_POSTGRES_POOL_MAX the deployment carries, zero
	// when it sets none. The guest reports it; older guests omit it.
	PoolMax int `json:"pool_max,omitempty"`
	// Connections is filled by the CLI after a passing canary, never by the
	// guest, and does not change Passed.
	Connections *PostgresBindingConnections `json:"connections,omitempty"`
}

// Passed reports whether the injected environment, connection setup, database
// connection, and read-only query all succeeded.
func (r PostgresBindingProbeReport) Passed() bool {
	return r.Environment.Status == "passed" && r.Configuration.Status == "passed" &&
		r.Connection.Status == "passed" && r.Query.Status == "passed"
}

// PostgresBindingConnections compares the most connections one PostgreSQL
// binding's pools can hold across the whole app with the connections the
// database's qualified allocation allows (ADR-20261104141122 step 4). It is
// advice: a warning never fails verification.
type PostgresBindingConnections struct {
	Status    string `json:"status"`
	Detail    string `json:"detail,omitempty"`
	PoolMax   int    `json:"pool_max,omitempty"`
	Instances int    `json:"instances,omitempty"`
	Total     int    `json:"total,omitempty"`
	Usable    *int64 `json:"usable_connections,omitempty"`
}

// ParsePostgresPoolMax reads a GREGALE_POSTGRES_POOL_MAX value the way the
// SDKs do: zero for a missing, non-numeric or out-of-range value.
func ParsePostgresPoolMax(value string) int {
	n, err := strconv.Atoi(strings.TrimSpace(value))
	if err != nil || n < 1 || n > PostgresPoolMaxAccepted {
		return 0
	}
	return n
}

// AssessPostgresBindingConnections totals poolMax over the app's instance
// ceiling, max_concurrency + RolloutConcurrencyGrant (invariant 1). usable is
// the database's qualified connection allocation, nil where none exists.
func AssessPostgresBindingConnections(poolMax, maxConcurrency int, usable *int64) PostgresBindingConnections {
	if poolMax < 1 {
		return PostgresBindingConnections{Status: "not_checked", Detail: "the deployment sets no " + PostgresPoolMaxEnv + "; the app's own pool settings decide its size"}
	}
	if maxConcurrency < 1 {
		return PostgresBindingConnections{Status: "not_checked", PoolMax: poolMax, Detail: "the app's instance limit is unknown"}
	}
	instances := maxConcurrency + RolloutConcurrencyGrant
	out := PostgresBindingConnections{PoolMax: poolMax, Instances: instances, Total: instances * poolMax, Usable: usable}
	total := fmt.Sprintf("up to %d connections (%d instances × pool of %d)", out.Total, instances, poolMax)
	switch {
	case usable == nil:
		out.Status = "not_checked"
		out.Detail = total + "; the database has no qualified connection allocation to compare with"
	case int64(out.Total) > *usable:
		out.Status = "warning"
		out.Detail = fmt.Sprintf("%s is more than the %d the database's allocation allows; lower %s or the app's max concurrency", total, *usable, PostgresPoolMaxEnv)
	default:
		out.Status = "passed"
		out.Detail = fmt.Sprintf("%s fits in the %d the database's allocation allows, which its other consumers share", total, *usable)
	}
	return out
}
