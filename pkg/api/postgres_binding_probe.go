package api

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
}

// Passed reports whether the injected environment, connection setup, database
// connection, and read-only query all succeeded.
func (r PostgresBindingProbeReport) Passed() bool {
	return r.Environment.Status == "passed" && r.Configuration.Status == "passed" &&
		r.Connection.Status == "passed" && r.Query.Status == "passed"
}
