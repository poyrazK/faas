package migrations

import (
	_ "embed"
	"encoding/json"
	"errors"
)

// The reviewed manifest covers only frozen standards migrations. It never
// expands automatically to a newly embedded migration or a legacy version.
//
//go:embed application_standard_recovery_sources.json
var applicationStandardRecoveryManifest []byte

type ApplicationStandardRecoverySource struct {
	Source
	Postcondition string `json:"postcondition"`
}

func ApplicationStandardRecoverySources() (map[int64]ApplicationStandardRecoverySource, error) {
	var manifest struct {
		Format  string                              `json:"format"`
		Sources []ApplicationStandardRecoverySource `json:"sources"`
	}
	if err := json.Unmarshal(applicationStandardRecoveryManifest, &manifest); err != nil {
		return nil, err
	}
	if manifest.Format != "gregale.application-standard-ledger-recovery-sources.v1" || len(manifest.Sources) == 0 {
		return nil, errors.New("invalid standards recovery manifest")
	}
	sources, err := Sources()
	if err != nil {
		return nil, err
	}
	known := make(map[int64]Source, len(sources))
	for _, source := range sources {
		known[source.Version] = source
	}
	out := make(map[int64]ApplicationStandardRecoverySource, len(manifest.Sources))
	for _, candidate := range manifest.Sources {
		if !IsTimestampMigrationVersion(candidate.Version) || known[candidate.Version] != candidate.Source || out[candidate.Version].Version != 0 {
			return nil, errors.New("standards recovery source does not match frozen migration")
		}
		switch candidate.Postcondition {
		case "ddl-only", "enrollment-coverage", "materialized-source-coverage":
		default:
			return nil, errors.New("unknown standards recovery postcondition")
		}
		out[candidate.Version] = candidate
	}
	return out, nil
}
