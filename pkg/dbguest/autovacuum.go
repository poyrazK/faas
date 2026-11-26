package dbguest

type AutovacuumProfile string

const (
	AutovacuumDefault   AutovacuumProfile = "default"
	AutovacuumHighChurn AutovacuumProfile = "high-churn"
)

type AutovacuumSettings struct {
	VacuumScaleFactor  float64 `json:"vacuum_scale_factor"`
	AnalyzeScaleFactor float64 `json:"analyze_scale_factor"`
}

func (p AutovacuumProfile) Settings() (AutovacuumSettings, bool) {
	switch p {
	case AutovacuumDefault:
		return AutovacuumSettings{VacuumScaleFactor: 0.2, AnalyzeScaleFactor: 0.1}, true
	case AutovacuumHighChurn:
		return AutovacuumSettings{VacuumScaleFactor: 0.05, AnalyzeScaleFactor: 0.02}, true
	}
	return AutovacuumSettings{}, false
}

type AutovacuumStatus struct {
	Profile          AutovacuumProfile   `json:"profile"`
	State            string              `json:"state"`
	Effective        *AutovacuumSettings `json:"effective,omitempty"`
	ReplicaEffective *AutovacuumSettings `json:"replica_effective,omitempty"`
}

const AutovacuumApplied = "applied"
const AutovacuumPending = "pending"

const AutovacuumSQL = `
SELECT pg_catalog.json_build_object(
    'vacuum_scale_factor', pg_catalog.current_setting('autovacuum_vacuum_scale_factor')::float8,
    'analyze_scale_factor', pg_catalog.current_setting('autovacuum_analyze_scale_factor')::float8
);
`
